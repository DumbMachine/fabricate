// Package all is the single compile-time registry for official HTTP
// resources. The CLI, engine, and supervisor must receive this same object.
package all

import (
	"fmt"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/airbyte"
	"github.com/dumbmachine/fabricate/resources/asana"
	"github.com/dumbmachine/fabricate/resources/attio"
	"github.com/dumbmachine/fabricate/resources/bamboohr"
	"github.com/dumbmachine/fabricate/resources/canny"
	"github.com/dumbmachine/fabricate/resources/carta"
	"github.com/dumbmachine/fabricate/resources/chargebee"
	"github.com/dumbmachine/fabricate/resources/close"
	"github.com/dumbmachine/fabricate/resources/cloudflare"
	"github.com/dumbmachine/fabricate/resources/digitalocean"
	"github.com/dumbmachine/fabricate/resources/docusign"
	"github.com/dumbmachine/fabricate/resources/gmail"
	"github.com/dumbmachine/fabricate/resources/greenhouse"
	"github.com/dumbmachine/fabricate/resources/gupshup"
	"github.com/dumbmachine/fabricate/resources/hubspot"
	"github.com/dumbmachine/fabricate/resources/intercom"
	"github.com/dumbmachine/fabricate/resources/ironclad"
	"github.com/dumbmachine/fabricate/resources/mailchimp"
	"github.com/dumbmachine/fabricate/resources/mailgun"
	"github.com/dumbmachine/fabricate/resources/outreach"
	"github.com/dumbmachine/fabricate/resources/pipedrive"
	"github.com/dumbmachine/fabricate/resources/razorpay"
	"github.com/dumbmachine/fabricate/resources/resend"
	"github.com/dumbmachine/fabricate/resources/sendgrid"
	"github.com/dumbmachine/fabricate/resources/slack"
	"github.com/dumbmachine/fabricate/resources/statuspage"
	"github.com/dumbmachine/fabricate/resources/vanta"
	"github.com/dumbmachine/fabricate/resources/zohobooks"
)

func Registry() *httpresource.Registry {
	registry, err := httpresource.NewRegistry(
		airbyte.NewResource(),
		asana.NewResource(),
		attio.NewResource(),
		bamboohr.NewResource(),
		canny.NewResource(),
		carta.NewResource(),
		chargebee.NewResource(),
		close.NewResource(),
		cloudflare.NewResource(),
		digitalocean.NewResource(),
		docusign.NewResource(),
		gmail.NewResource(),
		greenhouse.NewResource(),
		gupshup.NewResource(),
		hubspot.NewResource(),
		intercom.NewResource(),
		ironclad.NewResource(),
		mailchimp.NewResource(),
		mailgun.NewResource(),
		outreach.NewResource(),
		pipedrive.NewResource(),
		razorpay.NewResource(),
		resend.NewResource(),
		sendgrid.NewResource(),
		slack.NewResource(),
		statuspage.NewResource(),
		vanta.NewResource(),
		zohobooks.NewResource(),
	)
	if err != nil {
		panic(fmt.Sprintf("official HTTP resource registry: %v", err))
	}
	return registry
}
