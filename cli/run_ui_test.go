package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dumbmachine/fabricate/console"
	"github.com/spf13/cobra"
)

func TestRunUIServesTheWrappedSandbox(t *testing.T) {
	opened := ""
	originalOpen := openConsoleFn
	openConsoleFn = func(raw string) error {
		opened = raw
		return nil
	}
	t.Cleanup(func() { openConsoleFn = originalOpen })

	originalUI, originalAddr, originalProxy, originalScenario := runUI, runUIAddr, runProxy, runScenario
	runUI = true
	runProxy = true
	runScenario = "gmail.minimal.v1"
	runUIAddr = "127.0.0.1:0"
	t.Cleanup(func() {
		runUI, runUIAddr, runProxy, runScenario = originalUI, originalAddr, originalProxy, originalScenario
	})

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStderr := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = originalStderr })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := &cobra.Command{}
	command.SetContext(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runEnvironmentCommand(command, []string{"gmail", "sleep", "30"})
		_ = writer.Close()
	}()

	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "fab: console ") {
				urlCh <- strings.TrimPrefix(line, "fab: console ")
				return
			}
		}
	}()

	var consoleURL string
	select {
	case err := <-errCh:
		cancel()
		t.Fatalf("run exited before the console was ready: %v", err)
	case consoleURL = <-urlCh:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("timed out waiting for the console")
	}
	if opened != consoleURL {
		t.Fatalf("opened %q, console %q", opened, consoleURL)
	}

	response, err := http.Get(consoleURL + "/api/sessions")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer response.Body.Close()
	var sessions []console.Session
	if err := json.NewDecoder(response.Body).Decode(&sessions); err != nil {
		cancel()
		t.Fatal(err)
	}
	if len(sessions) != 1 || !sessions[0].Anchored || !sessions[0].Proxy || sessions[0].Services[0].Name != "gmail" {
		cancel()
		t.Fatalf("sessions: %#v", sessions)
	}
	held := sessions[0]
	profile, err := http.NewRequest(http.MethodGet, consoleURL+"/s/"+held.ID+"/gmail/gmail/v1/users/me/profile", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	profile.Header.Set("Authorization", "Bearer "+held.Services[0].Token)
	profileResponse, err := http.DefaultClient.Do(profile)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	body, _ := io.ReadAll(profileResponse.Body)
	profileResponse.Body.Close()
	if profileResponse.StatusCode != http.StatusOK || !strings.Contains(string(body), "emailAddress") {
		cancel()
		t.Fatalf("profile %d: %s", profileResponse.StatusCode, body)
	}

	closeHeld, err := http.NewRequest(http.MethodDelete, consoleURL+"/api/sessions/"+held.ID, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	closeResponse, err := http.DefaultClient.Do(closeHeld)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	closeResponse.Body.Close()
	if closeResponse.StatusCode != http.StatusConflict {
		cancel()
		t.Fatalf("close held sandbox status %d", closeResponse.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Fatalf("run exit: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for fab run to exit")
	}
}

func TestBrowserCommandUsesThePlatformOpener(t *testing.T) {
	command, err := browserCommand("http://127.0.0.1:4173")
	if err != nil {
		if strings.Contains(err.Error(), "xdg-open is not available") {
			return
		}
		t.Fatal(err)
	}
	if command.Path == "" || len(command.Args) < 2 || command.Args[len(command.Args)-1] != "http://127.0.0.1:4173" {
		t.Fatalf("browser command: %#v", command)
	}
}
