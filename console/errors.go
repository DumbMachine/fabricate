package console

import "fmt"

type unknownEnvironmentError struct{ name string }

func (e unknownEnvironmentError) Error() string {
	return fmt.Sprintf("unknown environment %q", e.name)
}

func errUnknownEnvironment(name string) error {
	return unknownEnvironmentError{name: name}
}

type unknownScenarioError struct{ id string }

func (e unknownScenarioError) Error() string {
	return fmt.Sprintf("unknown scenario %q", e.id)
}

func errUnknownScenario(id string) error {
	return unknownScenarioError{id: id}
}
