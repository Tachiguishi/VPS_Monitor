package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type serverConfig struct {
	Name      string `json:"name"`
	ID        string `json:"veid"`
	APIKeyEnv string `json:"apiKeyEnv"`
	key       string
}

type config struct {
	Listen      string         `json:"listen"`
	Database    string         `json:"database"`
	PollSeconds int            `json:"pollIntervalSeconds"`
	Retention   int            `json:"retentionDays"`
	Servers     []serverConfig `json:"servers"`
	user        string
	password    string
}

func (c config) interval() time.Duration {
	return time.Duration(c.PollSeconds) * time.Second
}

var veidPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func loadConfig(path string, demo bool, listen string) (config, error) {
	c := config{Listen: "127.0.0.1:8080", Database: "data/monitor.db", PollSeconds: 300, Retention: 90}
	envFile, err := godotenv.Read(".env")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Parser errors may include the contents of the file.
		return c, errors.New("cannot read or parse .env")
	}
	getenv := func(name string) string {
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		return envFile[name]
	}
	if demo {
		c.Servers = []serverConfig{{Name: "东京节点", ID: "demo-tokyo"}, {Name: "洛杉矶节点", ID: "demo-la"}}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return c, err
		}
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid config: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return c, errors.New("config must contain one JSON object")
		}
		if len(c.Servers) == 0 || c.Database == "" || c.Database == ":memory:" {
			return c, errors.New("configure at least one VPS and a database file")
		}
		seen := make(map[string]bool)
		for i := range c.Servers {
			server := &c.Servers[i]
			server.Name = strings.TrimSpace(server.Name)
			if !veidPattern.MatchString(server.ID) || seen[server.ID] {
				return c, fmt.Errorf("server %d has an invalid or duplicate VEID", i+1)
			}
			seen[server.ID] = true
			if server.Name == "" {
				server.Name = "VPS " + server.ID
			}
			server.key = strings.TrimSpace(getenv(server.APIKeyEnv))
			if server.key == "" {
				return c, fmt.Errorf("server %d: set the environment variable named in apiKeyEnv", i+1)
			}
		}
	}
	if listen != "" {
		c.Listen = listen
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil || port == "" {
		return c, errors.New("listen must be a host:port address")
	}
	c.user, c.password = getenv("MONITOR_USER"), getenv("MONITOR_PASSWORD")
	if (c.user == "") != (c.password == "") || strings.Contains(c.user, ":") {
		return c, errors.New("set both MONITOR_USER (without a colon) and MONITOR_PASSWORD")
	}
	if c.user == "" && host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return c, errors.New("set MONITOR_USER and MONITOR_PASSWORD for a non-loopback listener")
	}
	if c.PollSeconds < 60 || c.PollSeconds > 86400 || c.Retention < 1 || c.Retention > 3650 {
		return c, errors.New("pollIntervalSeconds must be 60–86400 and retentionDays must be 1–3650")
	}
	return c, nil
}
