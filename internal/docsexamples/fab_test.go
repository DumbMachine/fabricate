package docsexamples

import (
	"strings"
	"testing"
)

func TestFabArgsDefaultRun(t *testing.T) {
	args, err := fabArgs("/tmp/acme-gmail.yaml", Spec{
		Proxy: true,
		Argv:  []string{"curl", "-sS", "https://gmail.googleapis.com/gmail/v1/users/me/messages"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "run /tmp/acme-gmail.yaml --proxy -- curl -sS https://gmail.googleapis.com/gmail/v1/users/me/messages"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestFabArgsEvalProxy(t *testing.T) {
	args, err := fabArgs("/tmp/acme-gmail.yaml", Spec{
		Invocation: "eval",
		Proxy:      true,
		Flags:      []string{"--expected", "examples/eval/gmail-trash-msg-0028/expected", "--output", "json"},
		Argv:       []string{"./examples/eval/gmail-trash-msg-0028/run-opencode.sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "eval /tmp/acme-gmail.yaml --proxy --expected examples/eval/gmail-trash-msg-0028/expected --output json -- ./examples/eval/gmail-trash-msg-0028/run-opencode.sh"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestFabArgsEval(t *testing.T) {
	args, err := fabArgs("/tmp/acme-gmail.yaml", Spec{
		Invocation: "eval",
		Flags:      []string{"--expected", "examples/eval/gmail-trash-msg-0028/expected", "--output", "json"},
		Argv:       []string{"./examples/eval/gmail-trash-msg-0028/solution/solve.sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	want := "eval /tmp/acme-gmail.yaml --expected examples/eval/gmail-trash-msg-0028/expected --output json -- ./examples/eval/gmail-trash-msg-0028/solution/solve.sh"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}
