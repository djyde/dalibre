package plugin

import (
	"fmt"
	"io"
	"net/http"
)

// fetchClient is the client behind the script's fetch: the plugin's own,
// with its cookies, whose responses stop at MaxFetchBody. Files go through
// the host's download instead, which streams them to disk.
func (p *Plugin) fetchClient() *http.Client {
	c := *p.client
	c.Transport = capBodies{p.client.Transport}
	return &c
}

// allowFetch approves each request of the script's fetch, redirects
// included, before it is sent: one to a host the plugin may reach, at most
// MaxRequests a call. It adds the user agent, and the credentials of
// Options.Authorize to a request the script made (not to a redirect, which
// may lead elsewhere).
func (p *Plugin) allowFetch(req *http.Request) error {
	if req.Response == nil {
		if p.requests.Add(1) > MaxRequests {
			return fmt.Errorf("%w: more than %d requests in one call", ErrHostNotAllowed, MaxRequests)
		}
	}
	if err := p.checkURL(req.URL); err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent+" "+p.Manifest.ID+"/"+p.Manifest.Version)
	if req.Response == nil && p.authorize != nil {
		p.authorize(req)
	}
	return nil
}

// capBodies ends a response body past MaxFetchBody with an error.
type capBodies struct{ next http.RoundTripper }

func (c capBodies) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(req)
	if err == nil {
		resp.Body = &cappedBody{ReadCloser: resp.Body, url: req.URL.Redacted(), left: MaxFetchBody}
	}
	return resp, err
}

type cappedBody struct {
	io.ReadCloser
	url  string
	left int64
}

func (b *cappedBody) Read(buf []byte) (int, error) {
	if b.left <= 0 {
		return 0, fmt.Errorf("the response from %s is over %d MB; return the file’s URL for the host to download", b.url, MaxFetchBody>>20)
	}
	if int64(len(buf)) > b.left {
		buf = buf[:b.left+1] // one byte more tells a body of exactly the cap from a longer one
	}
	n, err := b.ReadCloser.Read(buf)
	b.left -= int64(n)
	if b.left < 0 {
		return 0, fmt.Errorf("the response from %s is over %d MB; return the file’s URL for the host to download", b.url, MaxFetchBody>>20)
	}
	return n, err
}
