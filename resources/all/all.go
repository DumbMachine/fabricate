// Package all is the single compile-time registry for official HTTP
// resources. The CLI, engine, and supervisor must receive this same object.
package all

import (
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/asana"
	"github.com/dumbmachine/fabricate/resources/attio"
	"github.com/dumbmachine/fabricate/resources/close"
	"github.com/dumbmachine/fabricate/resources/github"
	"github.com/dumbmachine/fabricate/resources/gmail"
	"github.com/dumbmachine/fabricate/resources/hubspot"
	"github.com/dumbmachine/fabricate/resources/intercom"
	"github.com/dumbmachine/fabricate/resources/mailchimp"
	"github.com/dumbmachine/fabricate/resources/mailgun"
	"github.com/dumbmachine/fabricate/resources/pipedrive"
	"github.com/dumbmachine/fabricate/resources/resend"
	"github.com/dumbmachine/fabricate/resources/sendgrid"
)

func Registry() *httpresource.Registry {
	registry, err := httpresource.NewRegistry(
		asana.NewResource(),
		attio.NewResource(),
		close.NewResource(),
		github.NewResource(),
		gmail.NewResource(),
		hubspot.NewResource(),
		intercom.NewResource(),
		mailchimp.NewResource(),
		mailgun.NewResource(),
		pipedrive.NewResource(),
		resend.NewResource(),
		sendgrid.NewResource(),
	)
	if err != nil {
		panic(fmt.Sprintf("official HTTP resource registry: %v", err))
	}
	return registry
}
