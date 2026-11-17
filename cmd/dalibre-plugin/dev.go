package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dalibre/internal/mcp"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// cmdDev adds the plugin's folder to the running Dalibre, which loads it
// from there and reloads it as its files change, and reports each load
// until interrupted. It goes through the app's MCP server, as an agent
// writing a plugin does.
func cmdDev(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev", flag.ExitOnError)
	dir, err := filepath.Abs(parse(fs, args, "dir")[0])
	if err != nil {
		return err
	}
	m, err := plugin.ReadManifest(dir)
	if err != nil {
		return err
	}
	// The same checks as check, so a mistake shows here with its stack
	// rather than only as a failed load in the app.
	if _, err := plugin.Load(dir, plugin.Options{DataDir: filepath.Join(dir, ".data"), Logf: func(string, ...any) {}}); err != nil {
		report(err)
	}

	s := settings.Load(settings.DefaultPath())
	if !s.MCP.On || s.MCP.Token == "" {
		return errors.New("Dalibre’s MCP server is off: turn it on in Dalibre’s Settings › MCP, then run this again " +
			"(or add the folder by hand: Plugins › Add plugin › From a folder…)")
	}
	if s.MCP.ReadOnly {
		return errors.New("Dalibre lets agents only read (Settings › MCP); allow changes to add a plugin")
	}
	cs, err := mcp.Dial(ctx, mcp.Endpoint(s.MCP.ServerPort()), s.MCP.Token, "dalibre-plugin", version)
	if errors.Is(err, mcp.ErrNotRunning) {
		return errors.New("Dalibre isn’t running, or its MCP server is off: open Dalibre, then run this again")
	}
	if err != nil {
		return err
	}
	defer cs.Close()

	fmt.Printf("adding %s to Dalibre…\n", dir)
	var added struct {
		Plugin mcp.PluginInfo `json:"plugin"`
	}
	if err := mcp.CallTool(ctx, cs, "add_plugin", map[string]string{"path": dir}, &added); err != nil {
		return err
	}
	fmt.Println("watching for changes; Dalibre reloads the plugin as you save. Ctrl-C to stop (the plugin stays added).")

	last := ""
	for {
		var list mcp.PluginList
		if err := mcp.CallTool(ctx, cs, "list_plugins", nil, &list); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("lost Dalibre: %w", err)
		}
		line, detail := devStatus(list, m.ID, dir)
		if key := line + detail; key != last {
			last = key
			fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), line)
			if detail != "" {
				fmt.Println(detail)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

// devStatus is what Dalibre says of the plugin id from dir: a line, and
// the errors or what the user must do, indented.
func devStatus(list mcp.PluginList, id, dir string) (string, string) {
	// The id may have changed since dev started: find it by folder.
	i := slices.IndexFunc(list.Plugins, func(p mcp.PluginInfo) bool { return p.Folder == dir })
	if i < 0 {
		i = slices.IndexFunc(list.Plugins, func(p mcp.PluginInfo) bool { return p.ID == id })
	}
	var errs []string
	for _, e := range list.Errors {
		if strings.Contains(e, dir) || strings.Contains(e, id) {
			errs = append(errs, "  "+strings.ReplaceAll(e, "\n", "\n  "))
		}
	}
	if i < 0 {
		return "not loaded", strings.Join(errs, "\n")
	}
	p := list.Plugins[i]
	line := fmt.Sprintf("%s %s: %s", p.ID, p.Version, p.State)
	var detail []string
	switch p.State {
	case mcp.PluginLoaded:
		if len(p.Exports) > 0 {
			line += " (exports " + strings.Join(p.Exports, ", ") + ")"
		}
		if len(p.MissingSettings) > 0 {
			detail = append(detail, "  fill in its settings in Dalibre’s Plugins: "+strings.Join(p.MissingSettings, ", "))
		}
	case mcp.PluginNeedsApproval:
		detail = append(detail, "  allow it in Dalibre; it asks to:")
		for _, a := range p.Asks {
			detail = append(detail, "    "+a)
		}
	case mcp.PluginDisabled:
		detail = append(detail, "  turn it on in Dalibre’s Plugins")
	}
	detail = append(detail, errs...)
	return line, strings.Join(detail, "\n")
}

// placeholderID reports whether id is still the template's.
func placeholderID(id string) bool {
	return id == templateID || strings.HasPrefix(id, "com.example.")
}

// templateID is the id the GitHub template repository's plugin.json
// starts with.
const templateID = "com.example.my-plugin"
