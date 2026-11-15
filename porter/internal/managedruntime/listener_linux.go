//go:build linux

package managedruntime

import (
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func listenerInodes(table []byte, host string, port int) map[string]bool {
	result := map[string]bool{}
	want := net.ParseIP(host)
	for _, line := range strings.Split(string(table), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		address, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil || int(n) != port {
			continue
		}
		ip, err := hex.DecodeString(address)
		if err != nil || len(ip) != 4 && len(ip) != 16 {
			continue
		}
		// Linux prints each address word in host byte order on the supported
		// little-endian arm64/amd64 targets. Ports remain network byte order.
		for word := 0; word < len(ip); word += 4 {
			ip[word], ip[word+3] = ip[word+3], ip[word]
			ip[word+1], ip[word+2] = ip[word+2], ip[word+1]
		}
		if net.IP(ip).Equal(want) && fields[9] != "0" {
			result[fields[9]] = true
		}
	}
	return result
}

func ownedListener(group, unit, host string, port int) bool {
	base, err := groupPath(group, unit)
	if err != nil || base == "" {
		return false
	}
	table := "/proc/net/tcp"
	if strings.Contains(host, ":") {
		table = "/proc/net/tcp6"
	}
	b, err := smallFile(table, 4<<20)
	if err != nil {
		return false
	}
	inodes := listenerInodes(b, host, port)
	if len(inodes) == 0 {
		return false
	}
	pids, err := smallFile(filepath.Join(base, "cgroup.procs"), 128<<10)
	if err != nil {
		return false
	}
	list := strings.Fields(string(pids))
	if len(list) > 4096 {
		return false
	}
	for _, text := range list {
		pid, err := strconv.Atoi(text)
		if err != nil || pid <= 0 {
			return false
		}
		start, err := processStart(pid)
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", text, "fd")
		files, err := os.ReadDir(dir)
		if err != nil || len(files) > 65536 {
			continue
		}
		for _, f := range files {
			link, err := os.Readlink(filepath.Join(dir, f.Name()))
			if err != nil {
				continue
			}
			if strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") && inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				after, err := processStart(pid)
				return err == nil && after == start
			}
		}
	}
	return false
}
