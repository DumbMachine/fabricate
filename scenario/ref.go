package scenario

import "fmt"

type Ref struct {
	ID     string `yaml:"id,omitempty" json:"id,omitempty"`
	Path   string `yaml:"path,omitempty" json:"path,omitempty"`
	Digest string `yaml:"digest,omitempty" json:"digest,omitempty"`
}

func (r Ref) Validate() error {
	if (r.ID == "") == (r.Path == "") {
		return fmt.Errorf("scenario ref: exactly one of id or path is required")
	}
	if r.Digest != "" && len(r.Digest) != len("sha256:")+64 {
		return fmt.Errorf("scenario ref: digest must be sha256:<64 hex characters>")
	}
	return nil
}
