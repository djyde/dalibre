package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"

	"dalibre/internal/proc"
	"dalibre/internal/sysproxy"
)

// Limits on what dalibre.exec runs.
const (
	ExecTimeout = 60 * time.Second // when the script gives none
	MaxExecOut  = 16 << 20         // of stdout, and of stderr
)

// execOptions are dalibre.exec's options, as the prelude passes them.
type execOptions struct {
	Env     map[string]string `json:"env"`
	Stdin   *string           `json:"stdin"`
	Timeout float64           `json:"timeout"` // ms
}

// ExecResult is what dalibre.exec resolves to.
type ExecResult struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// hostExec is __host_exec(id, args, options, lineID): it runs one of the
// plugin's tools and settles the promise it returns with what the tool
// printed. With a lineID, each line goes to the prelude's line function as
// it comes.
func (p *Plugin) hostExec(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	var in [4]string
	for i := range in {
		if i < len(args) {
			s, err := r.ToString(args[i])
			if err != nil {
				return moejs.Undefined(), err
			}
			in[i] = s.GoString()
		}
	}
	promise, settle := p.loop.NewPromise()
	ctx := p.ctx
	loop, lineFn := p.loop, p.lineFn
	var lines func(stream, line string)
	if in[3] != "" && in[3] != "0" {
		id := in[3]
		lines = func(stream, line string) {
			loop.RunOnLoop(func(rt *moejs.Runtime) {
				rt.CallFunction(lineFn, moejs.Undefined(), moejs.String(id), moejs.String(stream), moejs.String(line))
			})
		}
	}
	go func() {
		res, err := p.exec(ctx, in[0], in[1], in[2], lines)
		out := map[string]any{"ok": err == nil, "value": res}
		if err != nil {
			kind := KindUnavailable
			var pe *Error
			if errors.As(err, &pe) {
				kind, err = pe.Kind, errors.New(pe.Msg)
			}
			out["kind"], out["message"] = kind, err.Error()
		}
		data, _ := json.Marshal(out)
		settle(func(*moejs.Runtime) (moejs.Value, error) { return moejs.String(string(data)), nil })
	}()
	return promise, nil
}

// exec runs tool id with the JSON-encoded args and options.
func (p *Plugin) exec(ctx context.Context, id, rawArgs, rawOpts string, lines func(stream, line string)) (*ExecResult, error) {
	invalid := func(msg string) error {
		return &Error{Plugin: p.Manifest.ID, Kind: KindInvalid, Msg: "dalibre.exec: " + msg}
	}
	if _, ok := p.Manifest.Tools[id]; !ok {
		return nil, invalid(fmt.Sprintf("%q isn’t one of the plugin’s tools", id))
	}
	exe := p.tools[id]
	if exe == "" {
		return nil, &Error{Plugin: p.Manifest.ID, Kind: KindUnavailable, Msg: fmt.Sprintf("dalibre.exec: %s isn’t on this computer yet", id)}
	}
	var argv []string
	if err := json.Unmarshal([]byte(rawArgs), &argv); err != nil {
		return nil, invalid("args must be an array of strings")
	}
	var o execOptions
	if rawOpts != "" && rawOpts != "null" {
		if err := json.Unmarshal([]byte(rawOpts), &o); err != nil {
			return nil, invalid("options must be { env?, stdin?, timeout?, onLine? }: " + jsonProblem(err))
		}
	}
	for i, a := range argv {
		argv[i] = p.mapPath(a)
	}
	timeout := ExecTimeout
	if o.Timeout > 0 {
		timeout = time.Duration(o.Timeout) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, argv...)
	proc.Group(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = p.workDir()
	cmd.Env = execEnv(o.Env)
	if o.Stdin != nil {
		cmd.Stdin = strings.NewReader(*o.Stdin)
	}
	var stdout, stderr capped
	var wg sync.WaitGroup
	var pipes []io.Closer
	attach := func(stream string, buf *capped) io.Writer {
		if lines == nil {
			return buf
		}
		pr, pw := io.Pipe()
		wg.Go(func() {
			proc.Lines(pr, func(l string) { lines(stream, l) })
			io.Copy(io.Discard, pr)
		})
		pipes = append(pipes, pw)
		return io.MultiWriter(buf, pw)
	}
	cmd.Stdout = attach("stdout", &stdout)
	cmd.Stderr = attach("stderr", &stderr)
	err := cmd.Run()
	for _, c := range pipes {
		c.Close()
	}
	wg.Wait()
	res := &ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, &Error{Plugin: p.Manifest.ID, Kind: KindTimeout, Msg: fmt.Sprintf("dalibre.exec: %s took over %s", id, timeout.Round(time.Second))}
	case ctx.Err() != nil:
		return nil, &Error{Plugin: p.Manifest.ID, Kind: KindTimeout, Msg: "dalibre.exec: canceled"}
	case errors.As(err, &exit):
		res.Code = exit.ExitCode()
	case err != nil:
		return nil, &Error{Plugin: p.Manifest.ID, Kind: KindUnavailable, Msg: fmt.Sprintf("dalibre.exec: couldn’t run %s: %v", id, err)}
	}
	return res, nil
}

// mapPath turns an argument naming a file in /work or /data into its
// real path; any other argument is passed as is.
func (p *Plugin) mapPath(a string) string {
	if p.dataDir == "" {
		return a
	}
	for _, dir := range []string{workFolder, dataFolder} {
		root := "/" + dir
		if a == root || strings.HasPrefix(a, root+"/") {
			clean := path.Clean(a)
			if clean != root && !strings.HasPrefix(clean, root+"/") {
				return a
			}
			return filepath.Join(p.dataDir, filepath.FromSlash(clean[1:]))
		}
	}
	return a
}

// workDir is where a tool runs: the plugin's /work.
func (p *Plugin) workDir() string {
	if p.dataDir == "" {
		return os.TempDir()
	}
	dir := filepath.Join(p.dataDir, workFolder)
	os.MkdirAll(dir, 0o700)
	return dir
}

// execEnv is the environment a tool gets: only what a program needs to
// run, the computer's proxy, and the script's own.
func execEnv(own map[string]string) []string {
	var env []string
	keep := []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "TZ", "TERM"}
	if runtime.GOOS == "windows" {
		keep = []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
			"APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "PROGRAMFILES", "TEMP", "TMP", "USERNAME", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE"}
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if slices.ContainsFunc(keep, func(x string) bool { return strings.EqualFold(x, k) }) || strings.HasPrefix(k, "LC_") {
			env = append(env, kv)
		}
	}
	env = append(env, sysproxy.Env()...)
	// Tools that update themselves are told not to, where Dalibre knows
	// the switch.
	env = append(env, "HOMEBREW_NO_AUTO_UPDATE=1", "NO_UPDATE_NOTIFIER=1", "DO_NOT_TRACK=1")
	for k, v := range own {
		if k != "" && !strings.ContainsAny(k, "=\x00") && !strings.Contains(v, "\x00") {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// capped keeps what is written up to MaxExecOut, dropping the rest.
type capped struct{ bytes.Buffer }

func (c *capped) Write(b []byte) (int, error) {
	if room := MaxExecOut - c.Len(); room > 0 {
		if len(b) > room {
			c.Buffer.Write(b[:room])
		} else {
			c.Buffer.Write(b)
		}
	}
	return len(b), nil
}

// Check asks the plugin whether it is ready, when it exports check: its
// tools may be there and still not usable (signed out, say). The error
// says what is missing, by its kind (KindAuth, KindSettings).
func (p *Plugin) Check(ctx context.Context) error {
	if !p.Defines("check") {
		return nil
	}
	_, err := p.call(ctx, "check")
	return err
}
