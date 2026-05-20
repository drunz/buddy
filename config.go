package main

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DNSPort          string
	DNSZones         []string // empty means no zone filter
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

	publish := strings.ToLower(getenv("PUBLISH_IP", "container"))
	if publish != "host" && publish != "container" {
		publish = "container"
	}

	return Config{
		DNSPort:          getenv("DNS_PORT", "53"),
		DNSZones:         loadZones(),
		DNSTTL:           uint32(ttl),
		DockerHost:       getenv("DOCKER_HOST", "unix:///var/run/docker.sock"),
		CaddyLabelPrefix: getenv("CADDY_LABEL_PREFIX", "caddy"),
		LogLevel:         strings.ToLower(getenv("LOG_LEVEL", "info")),
		PublishIP:        publish,
		HostIP:           getenv("HOST_IP", ""),
	}
}

// loadZones parses DNS_ZONES as a comma-separated list of zones. If the env
// var is unset, it defaults to "local.lan.". If set to an empty (or
// whitespace-only) string, returns an empty slice — meaning no zone filter is
// applied to caddy labels.
func loadZones() []string {
	raw, ok := os.LookupEnv("DNS_ZONES")
	if !ok {
		return []string{"local.lan."}
	}
	var zones []string
	for _, z := range strings.Split(raw, ",") {
		z = strings.TrimSpace(z)
		if z == "" {
			continue
		}
		if !strings.HasSuffix(z, ".") {
			z += "."
		}
		zones = append(zones, z)
	}
	return zones
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
