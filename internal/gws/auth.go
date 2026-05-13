package gws

const (
	driveScope     = "https://www.googleapis.com/auth/drive.readonly"
	gmailScope     = "https://www.googleapis.com/auth/gmail.readonly"
	calendarScope  = "https://www.googleapis.com/auth/calendar.readonly"
	contactsScope  = "https://www.googleapis.com/auth/contacts.readonly"
	directoryScope = "https://www.googleapis.com/auth/admin.directory.user.readonly"
)

var serviceScopes = map[string][]string{
	"drive":    {driveScope},
	"gmail":    {gmailScope},
	"calendar": {calendarScope},
	"contacts": {contactsScope},
}

func ScopesForService(service string) []string {
	return serviceScopes[service]
}

func AllScopes() []string {
	return []string{driveScope, gmailScope, calendarScope, contactsScope, directoryScope}
}

type AuthConfig struct {
	ServiceAccountFile string
	AdminEmail         string
}
