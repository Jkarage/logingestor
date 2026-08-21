package providers

import (
	"github.com/jkarage/logingestor/business/domain/integrationbus"
	emailer "github.com/jkarage/logingestor/foundation/email"
)

// All returns every provider the service can deliver through, keyed by the id
// used in the integration_providers catalog.
//
// This is the authority on what is configurable: integrationbus.Create refuses
// an id with no caller, so a catalog row without an entry here is an option the
// UI offers and the API then rejects. The two are checked against each other by
// a test in the migrate package, which is where the catalog rows live.
func All(mailer *emailer.Config) map[string]integrationbus.Caller {
	return map[string]integrationbus.Caller{
		"slack":     NewSlack(),
		"discord":   NewDiscord(),
		"telegram":  NewTelegram(),
		"pagerduty": NewPagerDuty(),
		"webhook":   NewWebhook(),
		"email":     NewEmail(mailer),
		"opsgenie":  NewOpsGenie(),
		"jira":      NewJira(),
		"twilio":    NewTwilio(),
		"beemsms":   NewBeemSMS(),

		"msteams":        NewMSTeams(),
		"googlechat":     NewGoogleChat(),
		"mattermost":     NewMattermost(),
		"whatsapp":       NewWhatsApp(),
		"github":         NewGitHub(),
		"linear":         NewLinear(),
		"africastalking": NewAfricasTalking(),
	}
}
