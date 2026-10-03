package slack

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/slack/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const maxMessageText = 40000

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	handler http.Handler
}

type statusError struct{ code string }

func (e statusError) Error() string { return e.code }

func fail(code string) error { return statusError{code: code} }

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("slack: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("slack: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("slack: synthetic token is empty")
	}
	spec, err := generated.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("slack: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			request := input.RequestValidationInput.Request
			header := request.Header.Get("Authorization")
			if header != "" {
				if header == "Bearer "+token {
					return nil
				}
				return input.NewError(errors.New("invalid_auth"))
			}
			presented := presentedToken(request)
			if presented == token {
				return nil
			}
			if presented == "" {
				return input.NewError(errors.New("not_authed"))
			}
			return input.NewError(errors.New("invalid_auth"))
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			code := "invalid_arguments"
			if status == http.StatusUnauthorized {
				code = "invalid_auth"
				if strings.Contains(err.Error(), "not_authed") {
					code = "not_authed"
				}
			}
			writeSlackError(w, status, code)
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) AuthTest(ctx context.Context, _ generated.AuthTestRequestObject) (generated.AuthTestResponseObject, error) {
	team, err := s.loadTeam(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	return generated.AuthTest200JSONResponse{
		Ok: true, Url: team.URL, Team: team.Name, User: user.Name, TeamId: team.ID, UserId: user.ID,
	}, nil
}

func (s *server) UsersList(ctx context.Context, request generated.UsersListRequestObject) (generated.UsersListResponseObject, error) {
	users, err := s.loadUsers(ctx)
	if err != nil {
		return nil, err
	}
	start, end, next, err := pageBounds(len(users), request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return usersListError(err)
	}
	team, err := s.loadTeam(ctx)
	if err != nil {
		return nil, err
	}
	updated := int(s.clock.Now().UTC().Unix())
	members := []generated.User{}
	for _, user := range users[start:end] {
		members = append(members, user.api(team.ID, updated))
	}
	response := generated.UsersListResponse{Ok: true, Members: members, CacheTs: updated}
	if next != "" {
		response.ResponseMetadata = &generated.ResponseMetadata{NextCursor: next}
	}
	return generated.UsersList200JSONResponse(response), nil
}

func (s *server) UsersInfo(ctx context.Context, request generated.UsersInfoRequestObject) (generated.UsersInfoResponseObject, error) {
	user, err := s.loadUser(ctx, request.Params.User)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.UsersInfodefaultJSONResponse{Body: slackBody("user_not_found"), StatusCode: http.StatusOK}, nil
	}
	if err != nil {
		return nil, err
	}
	team, err := s.loadTeam(ctx)
	if err != nil {
		return nil, err
	}
	return generated.UsersInfo200JSONResponse{Ok: true, User: user.api(team.ID, int(s.clock.Now().UTC().Unix()))}, nil
}

func (s *server) ConversationsList(ctx context.Context, request generated.ConversationsListRequestObject) (generated.ConversationsListResponseObject, error) {
	rows, err := s.loadChannels(ctx)
	if err != nil {
		return nil, err
	}
	current, err := s.currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	filtered := []channelRow{}
	for _, channel := range rows {
		if request.Params.ExcludeArchived != nil && *request.Params.ExcludeArchived && channel.IsArchived {
			continue
		}
		if !allowsChannel(request.Params.Types, channel.IsPrivate) {
			continue
		}
		filtered = append(filtered, channel)
	}
	start, end, next, err := pageBounds(len(filtered), request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return conversationsListError(err)
	}
	channels := []generated.Conversation{}
	for _, channel := range filtered[start:end] {
		item, err := s.conversation(ctx, channel, current)
		if err != nil {
			return nil, err
		}
		channels = append(channels, item)
	}
	response := generated.ConversationsListResponse{Ok: true, Channels: channels}
	if next != "" {
		response.ResponseMetadata = &generated.ResponseMetadata{NextCursor: next}
	}
	return generated.ConversationsList200JSONResponse(response), nil
}

func (s *server) ConversationsInfo(ctx context.Context, request generated.ConversationsInfoRequestObject) (generated.ConversationsInfoResponseObject, error) {
	channel, err := s.loadChannel(ctx, request.Params.Channel)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ConversationsInfodefaultJSONResponse{Body: slackBody("channel_not_found"), StatusCode: http.StatusOK}, nil
	}
	if err != nil {
		return nil, err
	}
	current, err := s.currentUserID(ctx)
	if err != nil {
		return nil, err
	}
	item, err := s.conversation(ctx, channel, current)
	if err != nil {
		return nil, err
	}
	return generated.ConversationsInfo200JSONResponse{Ok: true, Channel: item}, nil
}

func (s *server) ConversationsMembers(ctx context.Context, request generated.ConversationsMembersRequestObject) (generated.ConversationsMembersResponseObject, error) {
	if _, err := s.loadChannel(ctx, request.Params.Channel); errors.Is(err, sql.ErrNoRows) {
		return generated.ConversationsMembersdefaultJSONResponse{Body: slackBody("channel_not_found"), StatusCode: http.StatusOK}, nil
	} else if err != nil {
		return nil, err
	}
	members, err := s.channelMembers(ctx, request.Params.Channel)
	if err != nil {
		return nil, err
	}
	start, end, next, err := pageBounds(len(members), request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return generated.ConversationsMembersdefaultJSONResponse{Body: slackBody("invalid_cursor"), StatusCode: http.StatusOK}, nil
	}
	page := []string{}
	page = append(page, members[start:end]...)
	return generated.ConversationsMembers200JSONResponse{
		Ok: true, Members: page, ResponseMetadata: generated.ResponseMetadata{NextCursor: next},
	}, nil
}

func (s *server) ConversationsHistory(ctx context.Context, request generated.ConversationsHistoryRequestObject) (generated.ConversationsHistoryResponseObject, error) {
	if err := checkBounds(request.Params.Oldest, request.Params.Latest); err != nil {
		return historyError(err)
	}
	if _, err := s.loadChannel(ctx, request.Params.Channel); errors.Is(err, sql.ErrNoRows) {
		return generated.ConversationsHistorydefaultJSONResponse{Body: slackBody("channel_not_found"), StatusCode: http.StatusOK}, nil
	} else if err != nil {
		return nil, err
	}
	rows, err := s.queryMessages(ctx, `SELECT user_id, text, ts, thread_ts FROM messages
		WHERE channel_id=? AND thread_ts='' ORDER BY ts DESC`, request.Params.Channel)
	if err != nil {
		return nil, err
	}
	filtered, err := filterMessages(rows, request.Params.Oldest, request.Params.Latest, boolValue(request.Params.Inclusive))
	if err != nil {
		return historyError(err)
	}
	start, end, next, err := pageBounds(len(filtered), request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return historyError(err)
	}
	messages := []generated.Message{}
	for _, row := range filtered[start:end] {
		count, err := s.replyCount(ctx, request.Params.Channel, row.Ts)
		if err != nil {
			return nil, err
		}
		messages = append(messages, apiMessage(row, count))
	}
	response := generated.HistoryResponse{Ok: true, Messages: messages, HasMore: next != "", PinCount: 0}
	if next != "" {
		response.ResponseMetadata = &generated.ResponseMetadata{NextCursor: next}
	}
	return generated.ConversationsHistory200JSONResponse(response), nil
}

func (s *server) ConversationsReplies(ctx context.Context, request generated.ConversationsRepliesRequestObject) (generated.ConversationsRepliesResponseObject, error) {
	if err := checkBounds(request.Params.Oldest, request.Params.Latest); err != nil {
		return repliesError(err)
	}
	if _, err := s.loadChannel(ctx, request.Params.Channel); errors.Is(err, sql.ErrNoRows) {
		return generated.ConversationsRepliesdefaultJSONResponse{Body: slackBody("channel_not_found"), StatusCode: http.StatusOK}, nil
	} else if err != nil {
		return nil, err
	}
	target, err := s.loadMessage(ctx, request.Params.Channel, request.Params.Ts)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ConversationsRepliesdefaultJSONResponse{Body: slackBody("thread_not_found"), StatusCode: http.StatusOK}, nil
	}
	if err != nil {
		return nil, err
	}
	rootTS := target.Ts
	if target.ThreadTs != "" {
		rootTS = target.ThreadTs
	}
	parent, err := s.loadMessage(ctx, request.Params.Channel, rootTS)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.ConversationsRepliesdefaultJSONResponse{Body: slackBody("thread_not_found"), StatusCode: http.StatusOK}, nil
	}
	if err != nil {
		return nil, err
	}
	replies, err := s.queryMessages(ctx, `SELECT user_id, text, ts, thread_ts FROM messages
		WHERE channel_id=? AND thread_ts=? ORDER BY ts ASC`, request.Params.Channel, rootTS)
	if err != nil {
		return nil, err
	}
	combined := append([]messageRow{parent}, replies...)
	filtered, err := filterMessages(combined, request.Params.Oldest, request.Params.Latest, boolValue(request.Params.Inclusive))
	if err != nil {
		return repliesError(err)
	}
	start, end, next, err := pageBounds(len(filtered), request.Params.Limit, request.Params.Cursor)
	if err != nil {
		return repliesError(err)
	}
	count := len(replies)
	messages := []generated.Message{}
	for _, row := range filtered[start:end] {
		repliesOnRow := 0
		if row.Ts == rootTS {
			repliesOnRow = count
		}
		messages = append(messages, apiMessage(row, repliesOnRow))
	}
	response := generated.RepliesResponse{Ok: true, Messages: messages}
	if next != "" {
		hasMore := true
		response.HasMore = &hasMore
		response.ResponseMetadata = &generated.ResponseMetadata{NextCursor: next}
	}
	return generated.ConversationsReplies200JSONResponse(response), nil
}

func (s *server) ChatPostMessage(ctx context.Context, request generated.ChatPostMessageRequestObject) (generated.ChatPostMessageResponseObject, error) {
	body := request.JSONBody
	if body == nil {
		body = request.FormdataBody
	}
	if body == nil {
		return generated.ChatPostMessagedefaultJSONResponse{Body: slackBody("invalid_arguments"), StatusCode: http.StatusBadRequest}, nil
	}
	channel, err := s.writableChannel(ctx, body.Channel)
	if err != nil {
		return postError(err)
	}
	text := stringValue(body.Text)
	if strings.TrimSpace(text) == "" {
		return generated.ChatPostMessagedefaultJSONResponse{Body: slackBody("no_text"), StatusCode: http.StatusOK}, nil
	}
	if len(text) > maxMessageText {
		return generated.ChatPostMessagedefaultJSONResponse{Body: slackBody("msg_too_long"), StatusCode: http.StatusOK}, nil
	}
	thread, err := s.resolveThread(ctx, channel.ID, stringValue(body.ThreadTs))
	if err != nil {
		return postError(err)
	}
	user, err := s.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	ts, err := s.nextTS(ctx, channel.ID)
	if err != nil {
		return nil, err
	}
	row := messageRow{UserID: user.ID, Text: text, Ts: ts, ThreadTs: thread}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO messages(channel_id, user_id, text, ts, thread_ts) VALUES(?, ?, ?, ?, ?)`,
		channel.ID, row.UserID, row.Text, row.Ts, row.ThreadTs); err != nil {
		return nil, err
	}
	return generated.ChatPostMessage200JSONResponse{Ok: true, Channel: channel.ID, Ts: ts, Message: apiMessage(row, 0)}, nil
}

func (s *server) ChatUpdate(ctx context.Context, request generated.ChatUpdateRequestObject) (generated.ChatUpdateResponseObject, error) {
	body := request.JSONBody
	if body == nil {
		body = request.FormdataBody
	}
	if body == nil {
		return generated.ChatUpdatedefaultJSONResponse{Body: slackBody("invalid_arguments"), StatusCode: http.StatusBadRequest}, nil
	}
	text := stringValue(body.Text)
	if strings.TrimSpace(text) == "" {
		return generated.ChatUpdatedefaultJSONResponse{Body: slackBody("no_text"), StatusCode: http.StatusOK}, nil
	}
	if len(text) > maxMessageText {
		return generated.ChatUpdatedefaultJSONResponse{Body: slackBody("msg_too_long"), StatusCode: http.StatusOK}, nil
	}
	if _, err := s.writableChannel(ctx, body.Channel); err != nil {
		return updateError(err)
	}
	message, err := s.authoredMessage(ctx, body.Channel, body.Ts, "cant_update_message")
	if err != nil {
		return updateError(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET text=? WHERE channel_id=? AND ts=?`, text, body.Channel, message.Ts); err != nil {
		return nil, err
	}
	return generated.ChatUpdate200JSONResponse{
		Ok: true, Channel: body.Channel, Ts: message.Ts, Text: text, Message: generated.UpdateMessageBody{Text: text},
	}, nil
}

func (s *server) ChatDelete(ctx context.Context, request generated.ChatDeleteRequestObject) (generated.ChatDeleteResponseObject, error) {
	body := request.JSONBody
	if body == nil {
		body = request.FormdataBody
	}
	if body == nil {
		return generated.ChatDeletedefaultJSONResponse{Body: slackBody("invalid_arguments"), StatusCode: http.StatusBadRequest}, nil
	}
	if _, err := s.loadChannel(ctx, body.Channel); errors.Is(err, sql.ErrNoRows) {
		return generated.ChatDeletedefaultJSONResponse{Body: slackBody("channel_not_found"), StatusCode: http.StatusOK}, nil
	} else if err != nil {
		return nil, err
	}
	message, err := s.authoredMessage(ctx, body.Channel, body.Ts, "cant_delete_message")
	if err != nil {
		return deleteError(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE channel_id=? AND ts=?`, body.Channel, message.Ts); err != nil {
		return nil, err
	}
	return generated.ChatDelete200JSONResponse{Ok: true, Channel: body.Channel, Ts: message.Ts}, nil
}

func (s *server) writableChannel(ctx context.Context, id string) (channelRow, error) {
	channel, err := s.loadChannel(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return channelRow{}, fail("channel_not_found")
	}
	if err != nil {
		return channelRow{}, err
	}
	if channel.IsArchived {
		return channelRow{}, fail("is_archived")
	}
	userID, err := s.currentUserID(ctx)
	if err != nil {
		return channelRow{}, err
	}
	member, err := s.isMember(ctx, channel.ID, userID)
	if err != nil {
		return channelRow{}, err
	}
	if !member {
		return channelRow{}, fail("not_in_channel")
	}
	return channel, nil
}

func (s *server) authoredMessage(ctx context.Context, channel, ts, forbidden string) (messageRow, error) {
	message, err := s.loadMessage(ctx, channel, ts)
	if errors.Is(err, sql.ErrNoRows) {
		return messageRow{}, fail("message_not_found")
	}
	if err != nil {
		return messageRow{}, err
	}
	userID, err := s.currentUserID(ctx)
	if err != nil {
		return messageRow{}, err
	}
	if message.UserID != userID {
		return messageRow{}, fail(forbidden)
	}
	return message, nil
}

func (s *server) resolveThread(ctx context.Context, channel, thread string) (string, error) {
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return "", nil
	}
	target, err := s.loadMessage(ctx, channel, thread)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fail("thread_not_found")
	}
	if err != nil {
		return "", err
	}
	if target.ThreadTs == "" {
		return target.Ts, nil
	}
	if _, err := s.loadMessage(ctx, channel, target.ThreadTs); errors.Is(err, sql.ErrNoRows) {
		return "", fail("thread_not_found")
	} else if err != nil {
		return "", err
	}
	return target.ThreadTs, nil
}

func (s *server) nextTS(ctx context.Context, channel string) (string, error) {
	now := s.clock.Now().UTC()
	sec := now.Unix()
	micro := int64(now.Nanosecond() / 1000)
	for range 1000000 {
		ts := fmt.Sprintf("%d.%06d", sec, micro)
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE channel_id=? AND ts=?`, channel, ts).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return ts, nil
		}
		micro++
		if micro > 999999 {
			sec++
			micro = 0
		}
	}
	return "", fmt.Errorf("slack: timestamp space exhausted")
}

type teamRow struct {
	ID, Name, Domain, URL string
}

type userRow struct {
	ID, Name, RealName, Email, Phone, Title string
	IsOwner, IsAdmin, IsBot, Deleted        bool
}

type channelRow struct {
	ID, Name, Creator, Topic, Purpose string
	IsPrivate, IsArchived             bool
	Created                           int
}

type messageRow struct {
	UserID, Text, Ts, ThreadTs string
}

func (s *server) loadTeam(ctx context.Context) (teamRow, error) {
	var team teamRow
	err := s.db.QueryRowContext(ctx, `SELECT id, name, domain, url FROM team`).Scan(&team.ID, &team.Name, &team.Domain, &team.URL)
	return team, err
}

func (s *server) currentUserID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='currentUserId'`).Scan(&id)
	return id, err
}

func (s *server) currentUser(ctx context.Context) (userRow, error) {
	id, err := s.currentUserID(ctx)
	if err != nil {
		return userRow{}, err
	}
	return s.loadUser(ctx, id)
}

func (s *server) loadUsers(ctx context.Context) ([]userRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, real_name, email, phone, title, is_owner, is_admin, is_bot, deleted
		FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []userRow{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *server) loadUser(ctx context.Context, id string) (userRow, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, name, real_name, email, phone, title, is_owner, is_admin, is_bot, deleted
		FROM users WHERE id=?`, id))
}

func scanUser(row interface{ Scan(...any) error }) (userRow, error) {
	var user userRow
	var owner, admin, bot, deleted int
	err := row.Scan(&user.ID, &user.Name, &user.RealName, &user.Email, &user.Phone, &user.Title, &owner, &admin, &bot, &deleted)
	if err != nil {
		return userRow{}, err
	}
	user.IsOwner, user.IsAdmin, user.IsBot, user.Deleted = owner != 0, admin != 0, bot != 0, deleted != 0
	return user, nil
}

func (u userRow) api(teamID string, updated int) generated.User {
	first, last, _ := strings.Cut(strings.TrimSpace(u.RealName), " ")
	team := teamID
	return generated.User{
		Id: u.ID, Name: u.Name, RealName: u.RealName, Deleted: u.Deleted,
		IsAdmin: u.IsAdmin, IsOwner: u.IsOwner, IsPrimaryOwner: u.IsOwner, IsBot: u.IsBot,
		IsAppUser: false, Updated: updated, TeamId: teamID,
		Profile: generated.UserProfile{
			RealName: u.RealName, DisplayName: u.RealName,
			RealNameNormalized: strings.ToLower(u.RealName), DisplayNameNormalized: strings.ToLower(u.RealName),
			Email: u.Email, Phone: u.Phone, Title: u.Title, FirstName: first, LastName: last, Team: &team,
		},
	}
}

func (s *server) loadChannels(ctx context.Context) ([]channelRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, creator, is_private, is_archived, topic, purpose, created
		FROM channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []channelRow{}
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (s *server) loadChannel(ctx context.Context, id string) (channelRow, error) {
	return scanChannel(s.db.QueryRowContext(ctx, `SELECT id, name, creator, is_private, is_archived, topic, purpose, created
		FROM channels WHERE id=?`, id))
}

func scanChannel(row interface{ Scan(...any) error }) (channelRow, error) {
	var channel channelRow
	var private, archived int
	err := row.Scan(&channel.ID, &channel.Name, &channel.Creator, &private, &archived, &channel.Topic, &channel.Purpose, &channel.Created)
	if err != nil {
		return channelRow{}, err
	}
	channel.IsPrivate, channel.IsArchived = private != 0, archived != 0
	return channel, nil
}

func (s *server) channelMembers(ctx context.Context, channelID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM channel_members WHERE channel_id=? ORDER BY user_id`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []string{}
	for rows.Next() {
		var member string
		if err := rows.Scan(&member); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *server) isMember(ctx context.Context, channelID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_members WHERE channel_id=? AND user_id=?`, channelID, userID).Scan(&count)
	return count > 0, err
}

func (s *server) conversation(ctx context.Context, channel channelRow, currentUser string) (generated.Conversation, error) {
	members, err := s.channelMembers(ctx, channel.ID)
	if err != nil {
		return generated.Conversation{}, err
	}
	member := false
	for _, id := range members {
		if id == currentUser {
			member = true
			break
		}
	}
	topic := generated.TopicPurpose{Value: channel.Topic, Creator: channel.Creator, LastSet: channel.Created}
	return generated.Conversation{
		Id: channel.ID, Name: channel.Name, NameNormalized: strings.ToLower(channel.Name),
		Created: channel.Created, Creator: channel.Creator, IsChannel: true, IsPrivate: channel.IsPrivate,
		IsArchived: channel.IsArchived, IsMpim: false, IsIm: false, IsMember: member, IsGeneral: false,
		IsShared: false, IsOrgShared: false, NumMembers: len(members), Topic: topic, Purpose: generated.TopicPurpose{Value: channel.Purpose, Creator: channel.Creator, LastSet: channel.Created},
	}, nil
}

func (s *server) loadMessage(ctx context.Context, channel, ts string) (messageRow, error) {
	var row messageRow
	err := s.db.QueryRowContext(ctx, `SELECT user_id, text, ts, thread_ts FROM messages WHERE channel_id=? AND ts=?`, channel, ts).
		Scan(&row.UserID, &row.Text, &row.Ts, &row.ThreadTs)
	return row, err
}

func (s *server) queryMessages(ctx context.Context, query string, args ...any) ([]messageRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []messageRow{}
	for rows.Next() {
		var row messageRow
		if err := rows.Scan(&row.UserID, &row.Text, &row.Ts, &row.ThreadTs); err != nil {
			return nil, err
		}
		messages = append(messages, row)
	}
	return messages, rows.Err()
}

func (s *server) replyCount(ctx context.Context, channel, ts string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE channel_id=? AND thread_ts=?`, channel, ts).Scan(&count)
	return count, err
}

func apiMessage(row messageRow, replyCount int) generated.Message {
	message := generated.Message{Type: "message", User: row.UserID, Text: row.Text, Ts: row.Ts}
	if replyCount > 0 {
		message.ReplyCount = &replyCount
		thread := row.Ts
		message.ThreadTs = &thread
	} else if row.ThreadTs != "" {
		thread := row.ThreadTs
		message.ThreadTs = &thread
	}
	return message
}

func allowsChannel(types *string, private bool) bool {
	raw := "public_channel"
	if types != nil && strings.TrimSpace(*types) != "" {
		raw = *types
	}
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "public_channel":
			if !private {
				return true
			}
		case "private_channel":
			if private {
				return true
			}
		}
	}
	return false
}

type slackTS struct{ sec, micro int64 }

func parseTS(raw string) (slackTS, bool) {
	raw = strings.TrimSpace(raw)
	secPart, frac, hasFrac := strings.Cut(raw, ".")
	if raw == "" || strings.Contains(frac, ".") {
		return slackTS{}, false
	}
	sec, err := strconv.ParseInt(secPart, 10, 64)
	if err != nil || sec < 0 {
		return slackTS{}, false
	}
	var micro int64
	if hasFrac {
		if frac == "" || len(frac) > 6 {
			return slackTS{}, false
		}
		for _, r := range frac {
			if r < '0' || r > '9' {
				return slackTS{}, false
			}
		}
		padded := frac + "000000"
		micro, err = strconv.ParseInt(padded[:6], 10, 64)
		if err != nil {
			return slackTS{}, false
		}
	}
	return slackTS{sec: sec, micro: micro}, true
}

func (t slackTS) cmp(other slackTS) int {
	if t.sec != other.sec {
		if t.sec < other.sec {
			return -1
		}
		return 1
	}
	if t.micro != other.micro {
		if t.micro < other.micro {
			return -1
		}
		return 1
	}
	return 0
}

func checkBounds(oldest, latest *string) error {
	if _, present, ok := optionalTS(oldest); present && !ok {
		return fail("invalid_ts_oldest")
	}
	if _, present, ok := optionalTS(latest); present && !ok {
		return fail("invalid_ts_latest")
	}
	return nil
}

func optionalTS(raw *string) (slackTS, bool, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return slackTS{}, false, true
	}
	parsed, ok := parseTS(*raw)
	return parsed, true, ok
}

func filterMessages(rows []messageRow, oldest, latest *string, inclusive bool) ([]messageRow, error) {
	lower, hasLower, lowerOK := optionalTS(oldest)
	if hasLower && !lowerOK {
		return nil, fail("invalid_ts_oldest")
	}
	upper, hasUpper, upperOK := optionalTS(latest)
	if hasUpper && !upperOK {
		return nil, fail("invalid_ts_latest")
	}
	filtered := []messageRow{}
	for _, row := range rows {
		parsed, ok := parseTS(row.Ts)
		if !ok {
			return nil, fmt.Errorf("slack: stored timestamp %q is invalid", row.Ts)
		}
		if hasLower {
			cmp := parsed.cmp(lower)
			if inclusive && cmp < 0 || !inclusive && cmp <= 0 {
				continue
			}
		}
		if hasUpper {
			cmp := parsed.cmp(upper)
			if inclusive && cmp > 0 || !inclusive && cmp >= 0 {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	return filtered, nil
}

func pageBounds(n int, limit *int, cursor *string) (int, int, string, error) {
	start := 0
	if cursor != nil && *cursor != "" {
		value, err := strconv.Atoi(*cursor)
		if err != nil || value < 0 || value > n {
			return 0, 0, "", fail("invalid_cursor")
		}
		start = value
	}
	end := n
	if limit != nil && *limit > 0 && start+*limit < end {
		end = start + *limit
	}
	next := ""
	if end < n {
		next = strconv.Itoa(end)
	}
	return start, end, next, nil
}

func slackBody(code string) generated.ErrorResponse {
	return generated.ErrorResponse{Ok: false, Error: code}
}

func writeSlackError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(slackBody(code))
}

func apiStatus(err error) (statusError, bool) {
	var status statusError
	if errors.As(err, &status) {
		return status, true
	}
	return statusError{}, false
}

func usersListError(err error) (generated.UsersListResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.UsersListdefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func conversationsListError(err error) (generated.ConversationsListResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.ConversationsListdefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func historyError(err error) (generated.ConversationsHistoryResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.ConversationsHistorydefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func repliesError(err error) (generated.ConversationsRepliesResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.ConversationsRepliesdefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func postError(err error) (generated.ChatPostMessageResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.ChatPostMessagedefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func updateError(err error) (generated.ChatUpdateResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.ChatUpdatedefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func deleteError(err error) (generated.ChatDeleteResponseObject, error) {
	if status, ok := apiStatus(err); ok {
		return generated.ChatDeletedefaultJSONResponse{Body: slackBody(status.code), StatusCode: http.StatusOK}, nil
	}
	return nil, err
}

func presentedToken(request *http.Request) string {
	if request == nil {
		return ""
	}
	if value := strings.TrimSpace(request.URL.Query().Get("token")); value != "" {
		return value
	}
	if value := strings.TrimSpace(request.Header.Get("token")); value != "" {
		return value
	}
	return bodyToken(request)
}

func bodyToken(request *http.Request) string {
	if request.Body == nil || request.Body == http.NoBody || request.GetBody == nil {
		return ""
	}
	reader, err := request.GetBody()
	if err != nil {
		return ""
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil || len(data) == 0 {
		return ""
	}
	mediaType := request.Header.Get("Content-Type")
	switch {
	case strings.Contains(mediaType, "application/json"), mediaType == "" && data[0] == '{':
		var payload map[string]json.RawMessage
		if json.Unmarshal(data, &payload) != nil {
			return ""
		}
		raw, ok := payload["token"]
		if !ok {
			return ""
		}
		var token string
		if json.Unmarshal(raw, &token) != nil {
			return ""
		}
		return token
	case strings.Contains(mediaType, "application/x-www-form-urlencoded"):
		values, err := url.ParseQuery(string(data))
		if err != nil {
			return ""
		}
		return values.Get("token")
	default:
		return ""
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value *bool) bool { return value != nil && *value }
