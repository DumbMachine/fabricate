package canny

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"time"

	"github.com/dumbmachine/fabricate/scenario"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema.sql
var schemaSQL string

//go:embed scenario.schema.json
var scenarioSchema []byte

//go:embed scenarios/*.json
var builtInScenarios embed.FS

var compiledScenarioSchema = func() *jsonschema.Schema {
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(scenarioSchema))
	if err != nil {
		panic(fmt.Sprintf("canny: parse embedded scenario schema: %v", err))
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("canny-scenario.json", parsed); err != nil {
		panic(fmt.Sprintf("canny: add embedded scenario schema: %v", err))
	}
	compiled, err := compiler.Compile("canny-scenario.json")
	if err != nil {
		panic(fmt.Sprintf("canny: compile embedded scenario schema: %v", err))
	}
	return compiled
}()

type scenarioCodec struct{}

type fixtureState struct {
	Boards []fixtureBoard `json:"boards"`
	Users  []fixtureUser  `json:"users"`
	Posts  []fixturePost  `json:"posts"`
	Votes  []fixtureVote  `json:"votes"`
}

type fixtureBoard struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Created         string `json:"created"`
	IsPrivate       bool   `json:"isPrivate"`
	PrivateComments bool   `json:"privateComments"`
}

type fixtureUser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"isAdmin"`
	Created string `json:"created"`
	UserID  string `json:"userID"`
}

type fixturePost struct {
	ID              string `json:"id"`
	BoardID         string `json:"boardID"`
	AuthorID        string `json:"authorID"`
	ByID            string `json:"byID,omitempty"`
	Title           string `json:"title"`
	Details         string `json:"details"`
	Status          string `json:"status"`
	Created         string `json:"created"`
	StatusChangedAt string `json:"statusChangedAt"`
}

type fixtureVote struct {
	ID           string `json:"id"`
	PostID       string `json:"postID"`
	VoterID      string `json:"voterID"`
	ByID         string `json:"byID,omitempty"`
	Created      string `json:"created"`
	VotePriority string `json:"votePriority"`
}

func (scenarioCodec) Validate(_ context.Context, doc scenario.Document) error {
	if err := doc.ValidateEnvelope(); err != nil {
		return err
	}
	if doc.Resource != "canny" || doc.ResourceVersion != "v1" {
		return fmt.Errorf("canny scenario: expected resource canny v1, got %s %s", doc.Resource, doc.ResourceVersion)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc.State))
	if err != nil {
		return fmt.Errorf("canny scenario: decode state for schema validation: %w", err)
	}
	if err := compiledScenarioSchema.Validate(instance); err != nil {
		return fmt.Errorf("canny scenario: schema validation: %w", err)
	}
	state, err := decodeState(doc.State)
	if err != nil {
		return err
	}
	boards := map[string]fixtureBoard{}
	for i, board := range state.Boards {
		if _, err := parseTimestamp(board.Created); err != nil {
			return fmt.Errorf("canny scenario: boards[%d].created: %w", i, err)
		}
		if _, exists := boards[board.ID]; exists {
			return fmt.Errorf("canny scenario: duplicate board id %q", board.ID)
		}
		boards[board.ID] = board
	}
	users := map[string]fixtureUser{}
	emails := map[string]string{}
	userIDs := map[string]string{}
	for i, user := range state.Users {
		if _, err := parseTimestamp(user.Created); err != nil {
			return fmt.Errorf("canny scenario: users[%d].created: %w", i, err)
		}
		if _, err := mail.ParseAddress(user.Email); err != nil {
			return fmt.Errorf("canny scenario: users[%d].email: %w", i, err)
		}
		if _, exists := users[user.ID]; exists {
			return fmt.Errorf("canny scenario: duplicate user id %q", user.ID)
		}
		if previous, exists := emails[user.Email]; exists {
			return fmt.Errorf("canny scenario: users %q and %q share email %q", previous, user.ID, user.Email)
		}
		if user.UserID != "" {
			if previous, exists := userIDs[user.UserID]; exists {
				return fmt.Errorf("canny scenario: users %q and %q share userID %q", previous, user.ID, user.UserID)
			}
			userIDs[user.UserID] = user.ID
		}
		users[user.ID] = user
		emails[user.Email] = user.ID
	}
	posts := map[string]fixturePost{}
	for i, post := range state.Posts {
		if _, err := parseTimestamp(post.Created); err != nil {
			return fmt.Errorf("canny scenario: posts[%d].created: %w", i, err)
		}
		if _, err := parseTimestamp(post.StatusChangedAt); err != nil {
			return fmt.Errorf("canny scenario: posts[%d].statusChangedAt: %w", i, err)
		}
		if _, ok := boards[post.BoardID]; !ok {
			return fmt.Errorf("canny scenario: post %q references unknown board %q", post.ID, post.BoardID)
		}
		if _, ok := users[post.AuthorID]; !ok {
			return fmt.Errorf("canny scenario: post %q references unknown author %q", post.ID, post.AuthorID)
		}
		if post.ByID != "" {
			admin, ok := users[post.ByID]
			if !ok || !admin.IsAdmin {
				return fmt.Errorf("canny scenario: post %q byID must be an admin", post.ID)
			}
		}
		if _, exists := posts[post.ID]; exists {
			return fmt.Errorf("canny scenario: duplicate post id %q", post.ID)
		}
		posts[post.ID] = post
	}
	seenVotes := map[string]struct{}{}
	pairs := map[string]struct{}{}
	for i, vote := range state.Votes {
		if _, err := parseTimestamp(vote.Created); err != nil {
			return fmt.Errorf("canny scenario: votes[%d].created: %w", i, err)
		}
		if _, ok := posts[vote.PostID]; !ok {
			return fmt.Errorf("canny scenario: vote %q references unknown post %q", vote.ID, vote.PostID)
		}
		if _, ok := users[vote.VoterID]; !ok {
			return fmt.Errorf("canny scenario: vote %q references unknown voter %q", vote.ID, vote.VoterID)
		}
		if vote.ByID != "" {
			admin, ok := users[vote.ByID]
			if !ok || !admin.IsAdmin {
				return fmt.Errorf("canny scenario: vote %q byID must be an admin", vote.ID)
			}
		}
		if _, exists := seenVotes[vote.ID]; exists {
			return fmt.Errorf("canny scenario: duplicate vote id %q", vote.ID)
		}
		pair := vote.PostID + "\x00" + vote.VoterID
		if _, exists := pairs[pair]; exists {
			return fmt.Errorf("canny scenario: duplicate vote on post %q by %q", vote.PostID, vote.VoterID)
		}
		seenVotes[vote.ID] = struct{}{}
		pairs[pair] = struct{}{}
	}
	return nil
}

func (scenarioCodec) Initialize(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("canny scenario: initialize: %w", err)
	}
	return nil
}

func (codec scenarioCodec) Load(ctx context.Context, db *sql.DB, doc scenario.Document) error {
	if err := codec.Validate(ctx, doc); err != nil {
		return err
	}
	state, _ := decodeState(doc.State)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("canny scenario: begin load: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"votes", "posts", "users", "boards"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("canny scenario: clear %s: %w", table, err)
		}
	}
	for _, board := range state.Boards {
		if _, err := tx.ExecContext(ctx, `INSERT INTO boards(id, name, created, is_private, private_comments) VALUES(?, ?, ?, ?, ?)`,
			board.ID, board.Name, board.Created, boolInt(board.IsPrivate), boolInt(board.PrivateComments)); err != nil {
			return fmt.Errorf("canny scenario: insert board %s: %w", board.ID, err)
		}
	}
	for _, user := range state.Users {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, name, email, is_admin, created, user_id) VALUES(?, ?, ?, ?, ?, ?)`,
			user.ID, user.Name, user.Email, boolInt(user.IsAdmin), user.Created, user.UserID); err != nil {
			return fmt.Errorf("canny scenario: insert user %s: %w", user.ID, err)
		}
	}
	for _, post := range state.Posts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO posts(id, board_id, author_id, by_id, title, details, status, created, status_changed_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			post.ID, post.BoardID, post.AuthorID, post.ByID, post.Title, post.Details, post.Status, post.Created, post.StatusChangedAt); err != nil {
			return fmt.Errorf("canny scenario: insert post %s: %w", post.ID, err)
		}
	}
	for _, vote := range state.Votes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO votes(id, post_id, voter_id, by_id, created, vote_priority) VALUES(?, ?, ?, ?, ?, ?)`,
			vote.ID, vote.PostID, vote.VoterID, vote.ByID, vote.Created, vote.VotePriority); err != nil {
			return fmt.Errorf("canny scenario: insert vote %s: %w", vote.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("canny scenario: commit load: %w", err)
	}
	return nil
}

func (scenarioCodec) Dump(ctx context.Context, db *sql.DB, metadata scenario.Metadata) (scenario.Document, error) {
	var state fixtureState
	boardRows, err := db.QueryContext(ctx, `SELECT id, name, created, is_private, private_comments FROM boards ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("canny scenario: dump boards: %w", err)
	}
	for boardRows.Next() {
		var board fixtureBoard
		var isPrivate, privateComments int
		if err := boardRows.Scan(&board.ID, &board.Name, &board.Created, &isPrivate, &privateComments); err != nil {
			boardRows.Close()
			return scenario.Document{}, err
		}
		board.IsPrivate = isPrivate != 0
		board.PrivateComments = privateComments != 0
		state.Boards = append(state.Boards, board)
	}
	if err := boardRows.Err(); err != nil {
		boardRows.Close()
		return scenario.Document{}, err
	}
	if err := boardRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	userRows, err := db.QueryContext(ctx, `SELECT id, name, email, is_admin, created, user_id FROM users ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("canny scenario: dump users: %w", err)
	}
	for userRows.Next() {
		var user fixtureUser
		var isAdmin int
		if err := userRows.Scan(&user.ID, &user.Name, &user.Email, &isAdmin, &user.Created, &user.UserID); err != nil {
			userRows.Close()
			return scenario.Document{}, err
		}
		user.IsAdmin = isAdmin != 0
		state.Users = append(state.Users, user)
	}
	if err := userRows.Err(); err != nil {
		userRows.Close()
		return scenario.Document{}, err
	}
	if err := userRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	postRows, err := db.QueryContext(ctx, `SELECT id, board_id, author_id, by_id, title, details, status, created, status_changed_at FROM posts ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("canny scenario: dump posts: %w", err)
	}
	for postRows.Next() {
		var post fixturePost
		if err := postRows.Scan(&post.ID, &post.BoardID, &post.AuthorID, &post.ByID, &post.Title, &post.Details, &post.Status, &post.Created, &post.StatusChangedAt); err != nil {
			postRows.Close()
			return scenario.Document{}, err
		}
		state.Posts = append(state.Posts, post)
	}
	if err := postRows.Err(); err != nil {
		postRows.Close()
		return scenario.Document{}, err
	}
	if err := postRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	voteRows, err := db.QueryContext(ctx, `SELECT id, post_id, voter_id, by_id, created, vote_priority FROM votes ORDER BY id`)
	if err != nil {
		return scenario.Document{}, fmt.Errorf("canny scenario: dump votes: %w", err)
	}
	for voteRows.Next() {
		var vote fixtureVote
		if err := voteRows.Scan(&vote.ID, &vote.PostID, &vote.VoterID, &vote.ByID, &vote.Created, &vote.VotePriority); err != nil {
			voteRows.Close()
			return scenario.Document{}, err
		}
		state.Votes = append(state.Votes, vote)
	}
	if err := voteRows.Err(); err != nil {
		voteRows.Close()
		return scenario.Document{}, err
	}
	if err := voteRows.Close(); err != nil {
		return scenario.Document{}, err
	}
	if state.Boards == nil {
		state.Boards = []fixtureBoard{}
	}
	if state.Users == nil {
		state.Users = []fixtureUser{}
	}
	if state.Posts == nil {
		state.Posts = []fixturePost{}
	}
	if state.Votes == nil {
		state.Votes = []fixtureVote{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return scenario.Document{}, err
	}
	return scenario.Document{
		Contract: ContractName(), ContractVersion: 1, ID: metadata.ID,
		Resource: "canny", ResourceVersion: "v1", State: raw,
	}, nil
}

func decodeState(raw []byte) (fixtureState, error) {
	var state fixtureState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fixtureState{}, fmt.Errorf("canny scenario: decode state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fixtureState{}, fmt.Errorf("canny scenario: state has trailing data")
	}
	if state.Boards == nil || state.Users == nil || state.Posts == nil || state.Votes == nil {
		return fixtureState{}, fmt.Errorf("canny scenario: boards, users, posts, and votes are required arrays")
	}
	return state, nil
}

func parseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be RFC3339")
	}
	return parsed, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ContractName() string { return scenario.Contract }
