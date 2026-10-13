package engine

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/kloudkit/portico/internal/routes"
)

type Nginx struct {
	Listen   string
	Fallback int
	Temp     string
	CORS     []string
	Routes   []routes.Route
}

// NginxBinary returns the local nginx, or "" to run it in a container.
func NginxBinary() string {
	path, _ := exec.LookPath("nginx")
	return path
}

type nginxRoute struct {
	routes.Route
	ID int
}

func (n Nginx) Render() (string, error) {
	var out bytes.Buffer
	err := nginxTemplate.Execute(&out, n)

	return out.String(), err
}

func (n Nginx) Upstreams() []nginxRoute {
	rs := make([]nginxRoute, len(n.Routes))
	for i, r := range n.Routes {
		rs[i] = nginxRoute{Route: r, ID: i}
	}

	return rs
}

func (n Nginx) AnyOrigin() bool {
	return slices.Contains(n.CORS, "*")
}

func (n Nginx) HasRoot() bool {
	return slices.ContainsFunc(n.Routes, func(r routes.Route) bool { return r.Path == "/" })
}

var nginxTemplate = template.Must(template.New("nginx").Funcs(template.FuncMap{
	"stripped": func(r nginxRoute) bool { return r.Strip && r.Path != "/" },
	"quote":    func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` },
	"upstream": func(r nginxRoute) string { return fmt.Sprintf("portico_%d", r.ID) },
	"regex":    regexp.QuoteMeta,
}).Parse(`{{ define "location" }}
      proxy_http_version 1.1;
{{- if .PreserveHost }}
      proxy_set_header Host $http_host;
{{- else }}
      proxy_set_header Host localhost:{{ .Port }};
{{- end }}
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection $connection_upgrade;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Host $forwarded_host;
      proxy_set_header X-Forwarded-Proto $forwarded_proto;
{{- if stripped . }}
      proxy_set_header X-Forwarded-Prefix {{ .Path }};
      proxy_pass http://{{ upstream . }}$portico_path_{{ upstream . }};
{{- else }}
      proxy_pass http://{{ upstream . }};
{{- end }}
{{- end -}}
worker_processes 1;
error_log stderr warn;
pid {{ .Temp }}/nginx.pid;

events {
  worker_connections 1024;
}

http {
  access_log off;
  client_body_temp_path {{ .Temp }}/body;
  proxy_temp_path {{ .Temp }}/proxy;
  fastcgi_temp_path {{ .Temp }}/fastcgi;
  uwsgi_temp_path {{ .Temp }}/uwsgi;
  scgi_temp_path {{ .Temp }}/scgi;
  client_max_body_size 0;

  proxy_buffering off;
  proxy_request_buffering off;
  proxy_read_timeout 1d;
  proxy_send_timeout 1d;

  map $http_upgrade $connection_upgrade {
    default upgrade;
    "" "";
  }
  map $http_x_forwarded_host $forwarded_host {
    "" $http_host;
    default $http_x_forwarded_host;
  }
  map $http_x_forwarded_proto $forwarded_proto {
    "" $scheme;
    default $http_x_forwarded_proto;
  }

  map $http_origin $cors_origin {
    default "";
{{- if .AnyOrigin }}
    "~." $http_origin;
{{- else }}
{{- range .CORS }}
    {{ quote . }} $http_origin;
{{- end }}
{{- end }}
  }
  map $cors_origin $cors_credentials {
    "" "";
    default "true";
  }
  map "$request_method|$http_access_control_request_method" $preflight_method {
    "~^OPTIONS\|(?<method>.+)$" $method;
    default "";
  }
  map "$preflight_method|$cors_origin" $cors_preflight {
    "~^[^|]+\|.+$" 1;
    default 0;
  }
  map $cors_preflight $cors_max_age {
    1 600;
    default "";
  }
{{- range .Upstreams }}

  upstream {{ upstream . }} {
    server localhost:{{ .Port }} max_fails=0;
  }
{{- if stripped . }}
  map $request_uri $portico_path_{{ upstream . }} {
    "~^{{ regex .Path }}(?<rest>/[^?]*)(?<query>\?.*)?$" $rest$query;
    "~^{{ regex .Path }}(?<query>\?.*)?$" /$query;
  }
{{- end }}
{{- end }}

  server {
    listen {{ .Listen }};

    add_header Access-Control-Allow-Origin $cors_origin always;
    add_header Access-Control-Allow-Credentials $cors_credentials always;
    add_header Access-Control-Allow-Methods $preflight_method always;
    add_header Access-Control-Allow-Headers $http_access_control_request_headers always;
    add_header Access-Control-Max-Age $cors_max_age always;

    if ($cors_preflight) {
      return 204;
    }
{{- if .CORS }}

    proxy_hide_header Access-Control-Allow-Origin;
    proxy_hide_header Access-Control-Allow-Credentials;
    proxy_hide_header Access-Control-Allow-Methods;
    proxy_hide_header Access-Control-Allow-Headers;
    proxy_hide_header Access-Control-Max-Age;
{{- end }}

    error_page 502 504 = @portico;

    location @portico {
      proxy_pass http://127.0.0.1:{{ .Fallback }};
    }
{{- if not .HasRoot }}

    location / {
      proxy_pass http://127.0.0.1:{{ .Fallback }};
    }
{{- end }}
{{- range .Upstreams }}

    # {{ .Service }}
{{- if eq .Path "/" }}
    location / {
{{- template "location" . }}
    }
{{- else }}
    location = {{ .Path }} {
{{- template "location" . }}
    }
    location {{ .Path }}/ {
{{- template "location" . }}
    }
{{- end }}
{{- end }}
  }
}
`))
