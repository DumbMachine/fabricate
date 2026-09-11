package cli

import (
	"strings"
	"testing"
)

func TestEvalTargetAndCommand(t *testing.T) {
	target, command, err := evalTargetAndCommand([]string{"acme-gmail", "./solve.sh"})
	if err != nil || target != "acme-gmail" || strings.Join(command, " ") != "./solve.sh" {
		t.Fatalf("target=%q command=%q err=%v", target, command, err)
	}
	if _, _, err := evalTargetAndCommand([]string{"acme-gmail"}); err == nil {
		t.Fatal("expected missing command error")
	}
}

func TestFailMessage(t *testing.T) {
	if got := failMessage(false, true); got != "eval: live state failed field checks" {
		t.Fatalf("got %q", got)
	}
}
