package main

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DNSPort          string
	DNSZone          string
	DNSTTL           uint32
	DockerHost       string
	CaddyLabelPrefix string
	LogLevel         string
	PublishIP        string // "host" or "container"
	HostIP           string // explicit override; empty means auto-resolve
}

func LoadConfig() Config {
	ttl, err := strconv.Atoi(getenv("DNS_TTL", "30"))
	if err != nil || ttl < 0 {
		ttl = 30
	}

	zone := getenv("DNS_ZONE", "local.lan.")
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}

	publish := strings.ToLower(getenv("PUBLISH_IP", "host"))
	if publish != "host" && publish != "container" {
		publish = "host"
	}

	return Config{
		DNSPort:          getenv("DNS_PORT", "53"),
		DNSZone:          zone,
		DNSTTL:           uint32(ttl),
		DockerHost:       getenv("DOCKER_HOST", "unix:///var/run/docker.sock"),
		CaddyLabelPrefix: getenv("CADDY_LABEL_PREFIX", "caddy"),
		LogLevel:         strings.ToLower(getenv("LOG_LEVEL", "info")),
		PublishIP:        publish,
		HostIP:           getenv("HOST_IP", ""),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
