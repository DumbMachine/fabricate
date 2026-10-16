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

func TestHostedListenFollowsAPublicURL(t *testing.T) {
	hosted, err := resolveConsoleSettings("127.0.0.1:4173", false, "https://console.example.com/fab/", true, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if hosted.ListenAddr != "0.0.0.0:4173" || !hosted.Widened || hosted.PublicURL != "https://console.example.com/fab" || hosted.Proxy {
		t.Fatalf("hosted: %#v", hosted)
	}
	explicit, err := resolveConsoleSettings("127.0.0.1:9090", true, "https://console.example.com", true, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.ListenAddr != "127.0.0.1:9090" || explicit.Widened || !explicit.Proxy {
		t.Fatalf("explicit: %#v", explicit)
	}
	local, err := resolveConsoleSettings("127.0.0.1:4173", false, "http://localhost:4173", true, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if local.ListenAddr != "127.0.0.1:4173" || local.Widened {
		t.Fatalf("localhost public URL widened: %#v", local)
	}
}

func TestConsoleEnvConfiguresHosting(t *testing.T) {
	t.Setenv("FAB_CONSOLE_ADDR", "0.0.0.0:8080")
	t.Setenv("FAB_CONSOLE_PUBLIC_URL", "https://console.example.com/fab/")
	t.Setenv("FAB_CONSOLE_PROXY", "true")
	settings, err := resolveConsoleSettings("127.0.0.1:4173", false, "", false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ListenAddr != "0.0.0.0:8080" || settings.Widened || settings.PublicURL != "https://console.example.com/fab" || !settings.Proxy {
		t.Fatalf("env settings: %#v", settings)
	}

	t.Setenv("FAB_CONSOLE_PROXY", "sometimes")
	if _, err := resolveConsoleSettings("127.0.0.1:4173", false, "", false, false, false, false); err == nil {
		t.Fatal("invalid FAB_CONSOLE_PROXY accepted")
	}
}

func TestConsoleFlagWinsOverEnv(t *testing.T) {
	t.Setenv("FAB_CONSOLE_ADDR", "0.0.0.0:9")
	t.Setenv("FAB_CONSOLE_PUBLIC_URL", "https://env.example.com")
	t.Setenv("FAB_CONSOLE_PROXY", "true")
	settings, err := resolveConsoleSettings("127.0.0.1:4173", true, "", true, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ListenAddr != "127.0.0.1:4173" || settings.PublicURL != "" || settings.Proxy || settings.Widened {
		t.Fatalf("flag settings: %#v", settings)
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
