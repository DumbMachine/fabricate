package slack

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"regexp"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var (
	userIDPattern      = regexp.MustCompile(`^U-[a-z0-9-]+$`)
	channelIDPattern   = regexp.MustCompile(`^C-[a-z0-9-]+$`)
	teamIDPattern      = regexp.MustCompile(`^T-[a-z0-9-]+$`)
	tsPattern          = regexp.MustCompile(`^[0-9]{10}\.[0-9]{6}$`)
	channelNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)
	userNamePattern    = regexp.MustCompile(`^[a-z0-9._-]+$`)
)

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("slack: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("slack-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("slack: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("slack-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("slack: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Team          fixtureTeam      `json:"team"`
	CurrentUserID string           `json:"currentUserId"`
	Users         []fixtureUser    `json:"users"`
	Channels      []fixtureChannel `json:"channels"`
	Messages      []fixtureMessage `json:"messages"`
}

type fixtureTeam struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	URL    string `json:"url"`
}

type fixtureUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"realName"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Title    string `json:"title"`
	IsOwner  bool   `json:"isOwner"`
	IsAdmin  bool   `json:"isAdmin"`
	IsBot    bool   `json:"isBot"`
	Deleted  bool   `json:"deleted"`
}

type fixtureChannel struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Creator    string   `json:"creator"`
	IsPrivate  bool     `json:"isPrivate"`
	IsArchived bool     `json:"isArchived"`
	Topic      string   `json:"topic"`
	Purpose    string   `json:"purpose"`
	Created    int      `json:"created"`
	Members    []string `json:"members"`
}

type fixtureMessage struct {
	Channel  string `json:"channel"`
	User     string `json:"user"`
	Text     string `json:"text"`
	Ts       string `json:"ts"`
	ThreadTs string `json:"threadTs"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "slack" || doc.ResourceVersion != "v2" {
		return fmt.Errorf("slack scenario: expected resource slack v2, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("slack scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("slack scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	if !teamIDPattern.MatchString(state.Team.ID) || state.Team.Name == "" || state.Team.Domain == "" || state.Team.URL == "" {
		return fmt.Errorf("slack scenario: team requires id, name, domain, and url")
	}
	users := map[string]fixtureUser{}
	emails := map[string]struct{}{}
	owners := 0
	for i, user := range state.Users {
		if !userIDPattern.MatchString(user.ID) || !userNamePattern.MatchString(user.Name) || user.RealName == "" || user.Title == "" || user.Phone == "" {
			return fmt.Errorf("slack scenario: users[%d] is incomplete", i)
		}
		if _, err := mail.ParseAddress(user.Email); err != nil {
			return fmt.Errorf("slack scenario: users[%d].email: %w", i, err)
		}
		if _, exists := users[user.ID]; exists {
			return fmt.Errorf("slack scenario: duplicate user id %q", user.ID)
		}
		if _, exists := emails[user.Email]; exists {
			return fmt.Errorf("slack scenario: duplicate email %q", user.Email)
		}
		users[user.ID] = user
		emails[user.Email] = struct{}{}
		if user.IsOwner {
			owners++
		}
	}
	if owners != 1 {
		return fmt.Errorf("slack scenario: exactly one owner is required")
	}
	if _, ok := users[state.CurrentUserID]; !ok {
		return fmt.Errorf("slack scenario: currentUserId %q is not a seeded user", state.CurrentUserID)
	}
	channels := map[string]fixtureChannel{}
	names := map[string]struct{}{}
	for i, channel := range state.Channels {
		if !channelIDPattern.MatchString(channel.ID) || !channelNamePattern.MatchString(channel.Name) || channel.Topic == "" || channel.Purpose == "" || channel.Created < 0 {
			return fmt.Errorf("slack scenario: channels[%d] is incomplete", i)
		}
		if _, ok := users[channel.Creator]; !ok {
			return fmt.Errorf("slack scenario: channel %q creator %q is unknown", channel.ID, channel.Creator)
		}
		if _, exists := channels[channel.ID]; exists {
			return fmt.Errorf("slack scenario: duplicate channel id %q", channel.ID)
		}
		if _, exists := names[channel.Name]; exists {
			return fmt.Errorf("slack scenario: duplicate channel name %q", channel.Name)
		}
		seenMembers := map[string]struct{}{}
		for _, member := range channel.Members {
			if _, ok := users[member]; !ok {
				return fmt.Errorf("slack scenario: channel %q member %q is unknown", channel.ID, member)
			}
			if _, exists := seenMembers[member]; exists {
				return fmt.Errorf("slack scenario: channel %q repeats member %q", channel.ID, member)
			}
			seenMembers[member] = struct{}{}
		}
		channels[channel.ID] = channel
		names[channel.Name] = struct{}{}
	}
	type messageKey struct{ channel, ts string }
	seenMessages := map[messageKey]fixtureMessage{}
	for i, message := range state.Messages {
		if message.Text == "" || !tsPattern.MatchString(message.Ts) {
			return fmt.Errorf("slack scenario: messages[%d] requires text and ts", i)
		}
		if _, ok := channels[message.Channel]; !ok {
			return fmt.Errorf("slack scenario: message ts %s references unknown channel %q", message.Ts, message.Channel)
		}
		if _, ok := users[message.User]; !ok {
			return fmt.Errorf("slack scenario: message ts %s references unknown user %q", message.Ts, message.User)
		}
		if message.ThreadTs != "" && !tsPattern.MatchString(message.ThreadTs) {
			return fmt.Errorf("slack scenario: message ts %s has an invalid threadTs", message.Ts)
		}
		key := messageKey{message.Channel, message.Ts}
		if _, exists := seenMessages[key]; exists {
			return fmt.Errorf("slack scenario: duplicate message %s in %s", message.Ts, message.Channel)
		}
		seenMessages[key] = message
	}
	for _, message := range state.Messages {
		if message.ThreadTs == "" {
			continue
		}
		if message.ThreadTs == message.Ts {
			return fmt.Errorf("slack scenario: message %s cannot thread to itself", message.Ts)
		}
		parent, ok := seenMessages[messageKey{message.Channel, message.ThreadTs}]
		if !ok || parent.ThreadTs != "" {
			return fmt.Errorf("slack scenario: message %s threads to unknown parent %s", message.Ts, message.ThreadTs)
		}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("slack scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("slack scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"messages", "channel_members", "channels", "users", "team", "metadata"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("slack scenario: clear %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES('currentUserId', ?)", state.CurrentUserID); err != nil {
		return fmt.Errorf("slack scenario: insert metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO team(id, name, domain, url) VALUES(?, ?, ?, ?)", state.Team.ID, state.Team.Name, state.Team.Domain, state.Team.URL); err != nil {
		return fmt.Errorf("slack scenario: insert team: %w", err)
	}
	for _, user := range state.Users {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users
			(id, name, real_name, email, phone, title, is_owner, is_admin, is_bot, deleted)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, user.ID, user.Name, user.RealName, user.Email, user.Phone, user.Title,
			boolInt(user.IsOwner), boolInt(user.IsAdmin), boolInt(user.IsBot), boolInt(user.Deleted)); err != nil {
			return fmt.Errorf("slack scenario: insert user %s: %w", user.ID, err)
		}
	}
	for _, channel := range state.Channels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels
			(id, name, creator, is_private, is_archived, topic, purpose, created)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, channel.ID, channel.Name, channel.Creator, boolInt(channel.IsPrivate),
			boolInt(channel.IsArchived), channel.Topic, channel.Purpose, channel.Created); err != nil {
			return fmt.Errorf("slack scenario: insert channel %s: %w", channel.ID, err)
		}
		for _, member := range channel.Members {
			if _, err := tx.ExecContext(ctx, "INSERT INTO channel_members(channel_id, user_id) VALUES(?, ?)", channel.ID, member); err != nil {
				return fmt.Errorf("slack scenario: insert member %s of %s: %w", member, channel.ID, err)
			}
		}
	}
	for _, message := range state.Messages {
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(channel_id, user_id, text, ts, thread_ts)
			VALUES(?, ?, ?, ?, ?)`, message.Channel, message.User, message.Text, message.Ts, message.ThreadTs); err != nil {
			return fmt.Errorf("slack scenario: insert message %s: %w", message.Ts, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("slack scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='currentUserId'").Scan(&state.CurrentUserID); err != nil {
		return scenario.Document{}, fmt.Errorf("slack scenario: dump current user: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT id, name, domain, url FROM team").Scan(&state.Team.ID, &state.Team.Name, &state.Team.Domain, &state.Team.URL); err != nil {
		return scenario.Document{}, fmt.Errorf("slack scenario: dump team: %w", err)
	}
	userRows, err := db.QueryContext(ctx, `SELECT id, name, real_name, email, phone, title, is_owner, is_admin, is_bot, deleted
		FROM users ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("slack scenario: dump users: %w", err)
	}
	state.Users = []fixtureUser{}
	for userRows.Next() {
		var user fixtureUser
		var owner, admin, bot, deleted int
		if err := userRows.Scan(&user.ID, &user.Name, &user.RealName, &user.Email, &user.Phone, &user.Title, &owner, &admin, &bot, &deleted); err != nil {
			userRows.Close()
			return scenario.Document{}, err
		}
		user.IsOwner, user.IsAdmin, user.IsBot, user.Deleted = owner != 0, admin != 0, bot != 0, deleted != 0
		state.Users = append(state.Users, user)
	}
	if err := userRows.Err(); err != nil {
		userRows.Close()
		return scenario.Document{}, err
	}
	if err := userRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	channelRows, err := db.QueryContext(ctx, `SELECT id, name, creator, is_private, is_archived, topic, purpose, created
		FROM channels ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("slack scenario: dump channels: %w", err)
	}
	state.Channels = []fixtureChannel{}
	for channelRows.Next() {
		var channel fixtureChannel
		var private, archived int
		if err := channelRows.Scan(&channel.ID, &channel.Name, &channel.Creator, &private, &archived, &channel.Topic, &channel.Purpose, &channel.Created); err != nil {
			channelRows.Close()
			return scenario.Document{}, err
		}
		channel.IsPrivate, channel.IsArchived = private != 0, archived != 0
		channel.Members = []string{}
		state.Channels = append(state.Channels, channel)
	}
	if err := channelRows.Err(); err != nil {
		channelRows.Close()
		return scenario.Document{}, err
	}
	if err := channelRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	for i := range state.Channels {
		members, err := db.QueryContext(ctx, "SELECT user_id FROM channel_members WHERE channel_id=? ORDER BY user_id", state.Channels[i].ID)
		if err != nil {
			return scenario.Document{}, err
		}
		for members.Next() {
			var member string
			if err := members.Scan(&member); err != nil {
				members.Close()
				return scenario.Document{}, err
			}
			state.Channels[i].Members = append(state.Channels[i].Members, member)
		}
		if err := members.Err(); err != nil {
			members.Close()
			return scenario.Document{}, err
		}
		if err := members.Close(); err != nil {
			return scenario.Document{}, err
		}
	}
	messageRows, err := db.QueryContext(ctx, `SELECT channel_id, user_id, text, ts, thread_ts
		FROM messages ORDER BY channel_id, ts`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("slack scenario: dump messages: %w", err)
	}
	defer messageRows.Close()
	state.Messages = []fixtureMessage{}
	for messageRows.Next() {
		var message fixtureMessage
		if err := messageRows.Scan(&message.Channel, &message.User, &message.Text, &message.Ts, &message.ThreadTs); err != nil {
			return scenario.Document{}, err
		}
		state.Messages = append(state.Messages, message)
	}
	if err := messageRows.Err(); err != nil {
		return scenario.Document{}, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "slack", ResourceVersion: "v2", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("slack scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("slack scenario: state has trailing data")
	}
	if state.Users == nil || state.Channels == nil || state.Messages == nil {
		return fixtureState{}, fmt.Errorf("slack scenario: users, channels, and messages are required arrays")
	}
	for i, channel := range state.Channels {
		if channel.Members == nil {
			return fixtureState{}, fmt.Errorf("slack scenario: channels[%d].members is required", i)
		}
	}
	return state, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }
