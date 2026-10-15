package cli

import (
	"net"
	"testing"
)

func TestConsoleURLRewritesWildcardHosts(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:4173", "[::]:4173"} {
		url := consoleURL(&net.TCPAddr{IP: net.ParseIP(hostOf(t, addr)), Port: 4173})
		if url != "http://127.0.0.1:4173" {
			t.Fatalf("%s -> %s", addr, url)
		}
	}
	specific := consoleURL(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4173})
	if specific != "http://127.0.0.1:4173" {
		t.Fatalf("specific address -> %s", specific)
	}
}

func TestListenNetworkSelectsIPFamily(t *testing.T) {
	if listenNetwork("0.0.0.0:4173") != "tcp4" || listenNetwork("[::]:4173") != "tcp6" || listenNetwork("localhost:4173") != "tcp" {
		t.Fatalf("networks: %s %s %s", listenNetwork("0.0.0.0:4173"), listenNetwork("[::]:4173"), listenNetwork("localhost:4173"))
	}
}

func hostOf(t *testing.T, addr string) string {
	t.Helper()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return host
}
