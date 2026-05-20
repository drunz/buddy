package main

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ResolveHostIP returns the IPv4 address to publish in A records when
// PUBLISH_IP=host. Resolution order:
//  1. explicit override (HOST_IP)
//  2. host part of DOCKER_HOST if it's a tcp:// URL and resolves to a routable IP
//  3. default outbound interface (UDP dial trick — sends no packet)
func ResolveHostIP(override, dockerHost string) (string, error) {
	if override != "" {
		return override, nil
	}
	if ip := ipFromDockerHost(dockerHost); ip != "" {
		return ip, nil
	}
	return defaultOutboundIP()
}

func ipFromDockerHost(dockerHost string) string {
	if !strings.HasPrefix(dockerHost, "tcp://") && !strings.HasPrefix(dockerHost, "http://") && !strings.HasPrefix(dockerHost, "https://") {
		return ""
	}
	u, err := url.Parse(dockerHost)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

func defaultOutboundIP() (string, error) {
	conn, err := net.Dial("udp4", "192.0.2.1:80") // TEST-NET-1, no packet is sent
	if err != nil {
		return "", err
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return "", errors.New("could not determine default outbound IP")
	}
	return addr.IP.String(), nil
}
