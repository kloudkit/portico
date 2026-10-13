package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrNotRunning = errors.New("portico is not running here")

type Status struct {
	URL      string    `json:"url,omitempty"`
	Services []Service `json:"services"`
}

type Service struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	State    string  `json:"state"`
	ExitCode *int    `json:"exit_code,omitempty"`
	Health   string  `json:"health,omitempty"`
	Routes   []Route `json:"routes"`
}

type Route struct {
	Path  string `json:"path"`
	Port  int    `json:"port"`
	Ready bool   `json:"ready"`
}

// Socket is the control socket for a project. It lives in the user's runtime
// dir, or the temp dir, because unix socket paths are limited to about 100 characters.
func Socket(project string) string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}

	return filepath.Join(dir, "portico-"+project+".sock")
}

// Listen claims the socket, refusing if another portico already serves it.
func Listen(path string) (net.Listener, error) {
	if conn, err := net.Dial("unix", path); err == nil {
		conn.Close()
		return nil, errors.New("portico is already running for this config")
	}
	os.Remove(path)

	return net.Listen("unix", path)
}

type Client struct {
	path string
	http *http.Client
}

func NewClient(path string) *Client {
	return &Client{path: path, http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		},
	}}
}

func (c *Client) Running() bool {
	conn, err := net.Dial("unix", c.path)
	if err != nil {
		return false
	}
	conn.Close()

	return true
}

func (c *Client) Status() (Status, error) {
	var status Status
	err := c.do(http.MethodGet, "/status", &status)

	return status, err
}

func (c *Client) Restart(name string) error {
	return c.do(http.MethodPost, "/restart/"+name, nil)
}

func (c *Client) Stop() error {
	return c.do(http.MethodPost, "/stop", nil)
}

func (c *Client) do(method, path string, out any) error {
	if !c.Running() {
		return ErrNotRunning
	}

	req, _ := http.NewRequest(method, "http://portico"+path, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}

	return nil
}
