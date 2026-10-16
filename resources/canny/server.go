package canny

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/canny/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

const companyOrigin = "https://acme.canny.io"

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("canny: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("canny: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("canny: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("canny: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			// Body apiKey is intentionally ignored. The proxy overwrites
			// Authorization with the synthetic bearer token.
			header := input.RequestValidationInput.Request.Header.Get("Authorization")
			if header != "Bearer "+token {
				return input.NewError(errors.New("invalid api key"))
			}
			return nil
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			writeError(w, status, publicError(err))
		},
	})
	impl.handler = validator(generatedHandler)
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) CannyBoardsList(ctx context.Context, request generated.CannyBoardsListRequestObject) (generated.CannyBoardsListResponseObject, error) {
	if request.Body == nil {
		return boardListError("request body is required"), nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created, is_private, private_comments FROM boards ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boards := []generated.Board{}
	for rows.Next() {
		board, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		response, err := s.boardResponse(ctx, board)
		if err != nil {
			return nil, err
		}
		boards = append(boards, response)
	}
	return generated.CannyBoardsList200JSONResponse{Boards: boards}, rows.Err()
}

func (s *server) CannyBoardsRetrieve(ctx context.Context, request generated.CannyBoardsRetrieveRequestObject) (generated.CannyBoardsRetrieveResponseObject, error) {
	if request.Body == nil {
		return boardRetrieveError("request body is required"), nil
	}
	board, err := s.loadBoard(ctx, request.Body.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return boardRetrieveError("invalid board id"), nil
	}
	if err != nil {
		return nil, err
	}
	response, err := s.boardResponse(ctx, board)
	if err != nil {
		return nil, err
	}
	return generated.CannyBoardsRetrieve200JSONResponse(response), nil
}

func (s *server) CannyPostsList(ctx context.Context, request generated.CannyPostsListRequestObject) (generated.CannyPostsListResponseObject, error) {
	if request.Body == nil {
		return postListError("request body is required"), nil
	}
	body := request.Body
	sortKey := generated.Newest
	if body.Sort != nil {
		sortKey = *body.Sort
	}
	search := strings.ToLower(strings.TrimSpace(value(body.Search)))
	if sortKey == generated.Relevance && search == "" {
		return postListError("relevance sort requires search"), nil
	}
	rows, err := s.loadPostRows(ctx)
	if err != nil {
		return nil, err
	}
	statuses := statusSet(value(body.Status))
	authorID := strings.TrimSpace(value(body.AuthorID))
	boardID := strings.TrimSpace(value(body.BoardID))
	filtered := make([]rankedPost, 0, len(rows))
	for _, row := range rows {
		if boardID != "" && row.BoardID != boardID {
			continue
		}
		if authorID != "" && row.AuthorID != authorID {
			continue
		}
		if statuses != nil {
			if _, ok := statuses[row.Status]; !ok {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(row.Title+"\n"+row.Details), search) {
			continue
		}
		score, err := s.countVotes(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		filtered = append(filtered, rankedPost{row: row, score: score})
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return rankedLess(sortKey, filtered[i], filtered[j])
	})
	limit := 10
	if body.Limit != nil {
		limit = *body.Limit
	}
	skip := 0
	if body.Skip != nil {
		skip = *body.Skip
	}
	if skip > len(filtered) {
		skip = len(filtered)
	}
	end := skip + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	posts := []generated.Post{}
	for _, item := range filtered[skip:end] {
		post, err := s.postResponse(ctx, item.row)
		if err != nil {
			return nil, err
		}
		posts = append(posts, post)
	}
	return generated.CannyPostsList200JSONResponse{HasMore: end < len(filtered), Posts: posts}, nil
}

func (s *server) CannyPostsRetrieve(ctx context.Context, request generated.CannyPostsRetrieveRequestObject) (generated.CannyPostsRetrieveResponseObject, error) {
	if request.Body == nil {
		return postRetrieveError("request body is required"), nil
	}
	row, err := s.findPost(ctx, request.Body)
	if message, ok := clientMessage(err); ok {
		return postRetrieveError(message), nil
	}
	if err != nil {
		return nil, err
	}
	post, err := s.postResponse(ctx, row)
	if err != nil {
		return nil, err
	}
	return generated.CannyPostsRetrieve200JSONResponse(post), nil
}

func (s *server) CannyPostsCreate(ctx context.Context, request generated.CannyPostsCreateRequestObject) (generated.CannyPostsCreateResponseObject, error) {
	if request.Body == nil {
		return postCreateError("request body is required"), nil
	}
	body := request.Body
	title := strings.TrimSpace(body.Title)
	if title == "" {
		return postCreateError("title is required"), nil
	}
	if _, err := s.loadUser(ctx, body.AuthorID); errors.Is(err, sql.ErrNoRows) {
		return postCreateError("invalid author id"), nil
	} else if err != nil {
		return nil, err
	}
	if _, err := s.loadBoard(ctx, body.BoardID); errors.Is(err, sql.ErrNoRows) {
		return postCreateError("invalid board id"), nil
	} else if err != nil {
		return nil, err
	}
	byID := ""
	if body.ByID != nil && strings.TrimSpace(*body.ByID) != "" {
		admin, err := s.loadUser(ctx, strings.TrimSpace(*body.ByID))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !admin.IsAdmin) {
			return postCreateError("invalid by id"), nil
		}
		if err != nil {
			return nil, err
		}
		byID = admin.ID
	}
	created, err := s.optionalTime(body.Created)
	if err != nil {
		return postCreateError("invalid created"), nil
	}
	details := ""
	if body.Details != nil {
		details = *body.Details
	}
	id, err := s.ids.Next(ctx, "canny.post")
	if err != nil {
		return nil, fmt.Errorf("canny: allocate post ID: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO posts(id, board_id, author_id, by_id, title, details, status, created, status_changed_at)
		VALUES(?, ?, ?, ?, ?, ?, 'open', ?, ?)`, id, body.BoardID, body.AuthorID, byID, title, details, created, created)
	if err != nil {
		return nil, err
	}
	return generated.CannyPostsCreate200JSONResponse{Id: id}, nil
}

func (s *server) CannyPostsChangeStatus(ctx context.Context, request generated.CannyPostsChangeStatusRequestObject) (generated.CannyPostsChangeStatusResponseObject, error) {
	if request.Body == nil {
		return postStatusError("request body is required"), nil
	}
	body := request.Body
	status := strings.TrimSpace(body.Status)
	if status == "" {
		return postStatusError("status is required"), nil
	}
	changer, err := s.loadUser(ctx, body.ChangerID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !changer.IsAdmin) {
		return postStatusError("invalid changer id"), nil
	}
	if err != nil {
		return nil, err
	}
	row, err := s.loadPost(ctx, body.PostID)
	if errors.Is(err, sql.ErrNoRows) {
		return postStatusError("invalid post id"), nil
	}
	if err != nil {
		return nil, err
	}
	if row.Status != status {
		row.Status = status
		row.StatusChangedAt = formatTime(s.clock.Now())
		if _, err := s.db.ExecContext(ctx, `UPDATE posts SET status=?, status_changed_at=? WHERE id=?`, row.Status, row.StatusChangedAt, row.ID); err != nil {
			return nil, err
		}
	}
	post, err := s.postResponse(ctx, row)
	if err != nil {
		return nil, err
	}
	return generated.CannyPostsChangeStatus200JSONResponse(post), nil
}

func (s *server) CannyUsersRetrieve(ctx context.Context, request generated.CannyUsersRetrieveRequestObject) (generated.CannyUsersRetrieveResponseObject, error) {
	if request.Body == nil {
		return userRetrieveError("request body is required"), nil
	}
	body := request.Body
	id := strings.TrimSpace(value(body.Id))
	email := strings.TrimSpace(value(body.Email))
	userID := strings.TrimSpace(value(body.UserID))
	provided := 0
	for _, value := range []string{id, email, userID} {
		if value != "" {
			provided++
		}
	}
	if provided != 1 {
		return userRetrieveError("specify exactly one of id, email, or userID"), nil
	}
	var (
		user userRow
		err  error
	)
	switch {
	case id != "":
		user, err = s.loadUser(ctx, id)
	case email != "":
		user, err = s.findUser(ctx, `SELECT id, name, email, is_admin, created, user_id FROM users WHERE email=?`, email)
	default:
		user, err = s.findUser(ctx, `SELECT id, name, email, is_admin, created, user_id FROM users WHERE user_id=? AND user_id<>''`, userID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return userRetrieveError("invalid user id"), nil
	}
	if err != nil {
		return nil, err
	}
	return generated.CannyUsersRetrieve200JSONResponse(userResponse(user)), nil
}

func (s *server) CannyUsersCreateOrUpdate(ctx context.Context, request generated.CannyUsersCreateOrUpdateRequestObject) (generated.CannyUsersCreateOrUpdateResponseObject, error) {
	if request.Body == nil {
		return userWriteError("request body is required"), nil
	}
	body := request.Body
	id := strings.TrimSpace(value(body.Id))
	email := strings.TrimSpace(value(body.Email))
	userID := strings.TrimSpace(value(body.UserID))
	if id == "" && email == "" && userID == "" {
		return userWriteError("one of id, email, or userID is required"), nil
	}
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return userWriteError("invalid email"), nil
		}
	}
	name := ""
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
		if name == "" || utf8.RuneCountInString(name) > 50 {
			return userWriteError("name must be between 1 and 50 characters"), nil
		}
	}
	created := ""
	if body.Created != nil && strings.TrimSpace(*body.Created) != "" {
		parsed, err := parseTimestamp(strings.TrimSpace(*body.Created))
		if err != nil {
			return userWriteError("invalid created"), nil
		}
		created = formatTime(parsed)
	}
	current, err := s.matchUser(ctx, id, email, userID)
	if message, ok := clientMessage(err); ok {
		return userWriteError(message), nil
	}
	if err != nil {
		return nil, err
	}
	if current == nil {
		if name == "" {
			return userWriteError("name is required"), nil
		}
		if created == "" {
			created = formatTime(s.clock.Now())
		}
		newID, err := s.ids.Next(ctx, "canny.user")
		if err != nil {
			return nil, fmt.Errorf("canny: allocate user ID: %w", err)
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO users(id, name, email, is_admin, created, user_id) VALUES(?, ?, ?, 0, ?, ?)`,
			newID, name, email, created, userID)
		if err != nil {
			return nil, err
		}
		return generated.CannyUsersCreateOrUpdate200JSONResponse{Id: newID}, nil
	}
	if current.IsAdmin && ((name != "" && name != current.Name) || (email != "" && email != current.Email)) {
		return userWriteError("cannot modify admin"), nil
	}
	if name != "" {
		current.Name = name
	}
	if email != "" {
		current.Email = email
	}
	if userID != "" {
		current.UserID = userID
	}
	if created != "" {
		current.Created = created
	}
	_, err = s.db.ExecContext(ctx, `UPDATE users SET name=?, email=?, user_id=?, created=? WHERE id=?`,
		current.Name, current.Email, current.UserID, current.Created, current.ID)
	if err != nil {
		return nil, err
	}
	return generated.CannyUsersCreateOrUpdate200JSONResponse{Id: current.ID}, nil
}

func (s *server) CannyVotesCreate(ctx context.Context, request generated.CannyVotesCreateRequestObject) (generated.CannyVotesCreateResponseObject, error) {
	if request.Body == nil {
		return voteCreateError("request body is required"), nil
	}
	body := request.Body
	if _, err := s.loadPost(ctx, body.PostID); errors.Is(err, sql.ErrNoRows) {
		return voteCreateError("invalid post id"), nil
	} else if err != nil {
		return nil, err
	}
	if _, err := s.loadUser(ctx, body.VoterID); errors.Is(err, sql.ErrNoRows) {
		return voteCreateError("invalid voter id"), nil
	} else if err != nil {
		return nil, err
	}
	byID := ""
	if body.ByID != nil && strings.TrimSpace(*body.ByID) != "" {
		admin, err := s.loadUser(ctx, strings.TrimSpace(*body.ByID))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !admin.IsAdmin) {
			return voteCreateError("invalid by id"), nil
		}
		if err != nil {
			return nil, err
		}
		byID = admin.ID
	}
	priority := string(generated.NoPriority)
	if body.VotePriority != nil {
		switch *body.VotePriority {
		case generated.N0:
			priority = string(generated.NiceToHave)
		case generated.N10:
			priority = string(generated.Important)
		case generated.N20:
			priority = string(generated.MustHave)
		default:
			return voteCreateError("invalid vote priority"), nil
		}
	}
	created, err := s.optionalTime(body.Created)
	if err != nil {
		return voteCreateError("invalid created"), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM votes WHERE post_id=? AND voter_id=?`, body.PostID, body.VoterID).Scan(&existing)
	if err == nil {
		return generated.CannyVotesCreate200JSONResponse(generated.SuccessSuccess), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	id, err := s.ids.Next(ctx, "canny.vote")
	if err != nil {
		return nil, fmt.Errorf("canny: allocate vote ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO votes(id, post_id, voter_id, by_id, created, vote_priority) VALUES(?, ?, ?, ?, ?, ?)`,
		id, body.PostID, body.VoterID, byID, created, priority); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return generated.CannyVotesCreate200JSONResponse(generated.SuccessSuccess), nil
}

func (s *server) CannyVotesRetrieve(ctx context.Context, request generated.CannyVotesRetrieveRequestObject) (generated.CannyVotesRetrieveResponseObject, error) {
	if request.Body == nil {
		return voteRetrieveError("request body is required"), nil
	}
	vote, err := s.loadVote(ctx, request.Body.Id)
	if errors.Is(err, sql.ErrNoRows) {
		return voteRetrieveError("invalid vote id"), nil
	}
	if err != nil {
		return nil, err
	}
	response, err := s.voteResponse(ctx, vote)
	if err != nil {
		return nil, err
	}
	return generated.CannyVotesRetrieve200JSONResponse(response), nil
}

type boardRow struct {
	ID, Name, Created          string
	IsPrivate, PrivateComments bool
}

type userRow struct {
	ID, Name, Email, Created, UserID string
	IsAdmin                          bool
}

type postRow struct {
	ID, BoardID, AuthorID, ByID, Title, Details, Status, Created, StatusChangedAt string
}

type voteRow struct {
	ID, PostID, VoterID, ByID, Created, VotePriority string
}

type rankedPost struct {
	row   postRow
	score int
}

type clientError struct{ message string }

func (e clientError) Error() string { return e.message }

func clientMessage(err error) (string, bool) {
	var client clientError
	if errors.As(err, &client) {
		return client.message, true
	}
	return "", false
}

func (s *server) boardResponse(ctx context.Context, board boardRow) (generated.Board, error) {
	count, err := s.countPosts(ctx, board.ID)
	if err != nil {
		return generated.Board{}, err
	}
	return generated.Board{
		Id: board.ID, Name: board.Name, Created: board.Created,
		IsPrivate: board.IsPrivate, PrivateComments: board.PrivateComments,
		PostCount: count, Url: boardURL(board.Name),
	}, nil
}

func (s *server) postResponse(ctx context.Context, row postRow) (generated.Post, error) {
	author, err := s.loadUser(ctx, row.AuthorID)
	if err != nil {
		return generated.Post{}, err
	}
	board, err := s.loadBoard(ctx, row.BoardID)
	if err != nil {
		return generated.Post{}, err
	}
	count, err := s.countPosts(ctx, board.ID)
	if err != nil {
		return generated.Post{}, err
	}
	score, err := s.countVotes(ctx, row.ID)
	if err != nil {
		return generated.Post{}, err
	}
	post := generated.Post{
		Id: row.ID, Author: userResponse(author),
		Board: generated.BoardSummary{
			Id: board.ID, Name: board.Name, Created: board.Created, PostCount: count, Url: boardURL(board.Name),
		},
		CommentCount: 0, Created: row.Created, Details: row.Details, ImageURLs: []string{},
		Score: score, Status: row.Status, StatusChangedAt: row.StatusChangedAt,
		Title: row.Title, Url: postURL(board.Name, row.Title),
	}
	if row.ByID != "" {
		admin, err := s.loadUser(ctx, row.ByID)
		if err != nil {
			return generated.Post{}, err
		}
		copied := userResponse(admin)
		post.By = &copied
	}
	return post, nil
}

func (s *server) voteResponse(ctx context.Context, row voteRow) (generated.Vote, error) {
	post, err := s.loadPost(ctx, row.PostID)
	if err != nil {
		return generated.Vote{}, err
	}
	board, err := s.loadBoard(ctx, post.BoardID)
	if err != nil {
		return generated.Vote{}, err
	}
	voter, err := s.loadUser(ctx, row.VoterID)
	if err != nil {
		return generated.Vote{}, err
	}
	count, err := s.countPosts(ctx, board.ID)
	if err != nil {
		return generated.Vote{}, err
	}
	score, err := s.countVotes(ctx, post.ID)
	if err != nil {
		return generated.Vote{}, err
	}
	vote := generated.Vote{
		Id: row.ID, Created: row.Created, VotePriority: generated.VoteVotePriority(row.VotePriority),
		Board: generated.BoardSummary{
			Id: board.ID, Name: board.Name, Created: board.Created, PostCount: count, Url: boardURL(board.Name),
		},
		Post: generated.VotePost{
			Id: post.ID, CommentCount: 0, Details: post.Details, ImageURLs: []string{},
			Score: score, Status: post.Status, Title: post.Title, Url: postURL(board.Name, post.Title),
		},
		Voter: userResponse(voter),
	}
	if row.ByID != "" {
		admin, err := s.loadUser(ctx, row.ByID)
		if err != nil {
			return generated.Vote{}, err
		}
		copied := userResponse(admin)
		vote.By = &copied
	}
	return vote, nil
}

func userResponse(row userRow) generated.User {
	return generated.User{
		Id: row.ID, Name: row.Name, Email: row.Email, IsAdmin: row.IsAdmin,
		Created: row.Created, UserID: row.UserID, Url: userURL(row.Name),
	}
}

func (s *server) findPost(ctx context.Context, body *generated.RetrievePostRequest) (postRow, error) {
	if id := strings.TrimSpace(value(body.Id)); id != "" {
		row, err := s.loadPost(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return postRow{}, clientError{"invalid post id"}
		}
		return row, err
	}
	urlName := strings.TrimSpace(value(body.UrlName))
	if urlName == "" {
		return postRow{}, clientError{"id or urlName is required"}
	}
	boardID := strings.TrimSpace(value(body.BoardID))
	if boardID == "" {
		return postRow{}, clientError{"boardID is required"}
	}
	rows, err := s.loadPostRows(ctx)
	if err != nil {
		return postRow{}, err
	}
	for _, row := range rows {
		if row.BoardID == boardID && slug(row.Title) == urlName {
			return row, nil
		}
	}
	return postRow{}, clientError{"invalid url name"}
}

func (s *server) matchUser(ctx context.Context, id, email, userID string) (*userRow, error) {
	var current *userRow
	if id != "" {
		row, err := s.loadUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, clientError{"invalid user id"}
		}
		if err != nil {
			return nil, err
		}
		current = &row
	}
	if email != "" {
		row, err := s.findUser(ctx, `SELECT id, name, email, is_admin, created, user_id FROM users WHERE email=?`, email)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			if current != nil && current.ID != row.ID {
				return nil, clientError{"email belongs to another user"}
			}
			if current == nil {
				current = &row
			}
		}
	}
	if userID != "" {
		row, err := s.findUser(ctx, `SELECT id, name, email, is_admin, created, user_id FROM users WHERE user_id=? AND user_id<>''`, userID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			if current != nil && current.ID != row.ID {
				return nil, clientError{"userID belongs to another user"}
			}
			if current == nil {
				current = &row
			}
		}
	}
	return current, nil
}

func (s *server) loadBoard(ctx context.Context, id string) (boardRow, error) {
	return scanBoard(s.db.QueryRowContext(ctx, `SELECT id, name, created, is_private, private_comments FROM boards WHERE id=?`, id))
}

func (s *server) loadUser(ctx context.Context, id string) (userRow, error) {
	return s.findUser(ctx, `SELECT id, name, email, is_admin, created, user_id FROM users WHERE id=?`, id)
}

func (s *server) findUser(ctx context.Context, query string, arg string) (userRow, error) {
	return scanUser(s.db.QueryRowContext(ctx, query, arg))
}

func (s *server) loadPost(ctx context.Context, id string) (postRow, error) {
	return scanPost(s.db.QueryRowContext(ctx, `SELECT id, board_id, author_id, by_id, title, details, status, created, status_changed_at FROM posts WHERE id=?`, id))
}

func (s *server) loadPostRows(ctx context.Context) ([]postRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, board_id, author_id, by_id, title, details, status, created, status_changed_at FROM posts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []postRow{}
	for rows.Next() {
		row, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *server) loadVote(ctx context.Context, id string) (voteRow, error) {
	return scanVote(s.db.QueryRowContext(ctx, `SELECT id, post_id, voter_id, by_id, created, vote_priority FROM votes WHERE id=?`, id))
}

func (s *server) countPosts(ctx context.Context, boardID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE board_id=?`, boardID).Scan(&n)
	return n, err
}

func (s *server) countVotes(ctx context.Context, postID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM votes WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func (s *server) optionalTime(value *string) (string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return formatTime(s.clock.Now()), nil
	}
	parsed, err := parseTimestamp(strings.TrimSpace(*value))
	if err != nil {
		return "", err
	}
	return formatTime(parsed), nil
}

func scanBoard(row interface{ Scan(...any) error }) (boardRow, error) {
	var board boardRow
	var isPrivate, privateComments int
	err := row.Scan(&board.ID, &board.Name, &board.Created, &isPrivate, &privateComments)
	board.IsPrivate = isPrivate != 0
	board.PrivateComments = privateComments != 0
	return board, err
}

func scanUser(row interface{ Scan(...any) error }) (userRow, error) {
	var user userRow
	var isAdmin int
	err := row.Scan(&user.ID, &user.Name, &user.Email, &isAdmin, &user.Created, &user.UserID)
	user.IsAdmin = isAdmin != 0
	return user, err
}

func scanPost(row interface{ Scan(...any) error }) (postRow, error) {
	var post postRow
	err := row.Scan(&post.ID, &post.BoardID, &post.AuthorID, &post.ByID, &post.Title, &post.Details, &post.Status, &post.Created, &post.StatusChangedAt)
	return post, err
}

func scanVote(row interface{ Scan(...any) error }) (voteRow, error) {
	var vote voteRow
	err := row.Scan(&vote.ID, &vote.PostID, &vote.VoterID, &vote.ByID, &vote.Created, &vote.VotePriority)
	return vote, err
}

func rankedLess(sortKey generated.ListPostsRequestSort, a, b rankedPost) bool {
	switch sortKey {
	case generated.Oldest:
		if a.row.Created != b.row.Created {
			return a.row.Created < b.row.Created
		}
		return a.row.ID < b.row.ID
	case generated.Score:
		if a.score != b.score {
			return a.score > b.score
		}
		return a.row.ID < b.row.ID
	case generated.Trending:
		if a.score != b.score {
			return a.score > b.score
		}
		if a.row.Created != b.row.Created {
			return a.row.Created > b.row.Created
		}
		return a.row.ID < b.row.ID
	case generated.StatusChanged:
		if a.row.StatusChangedAt != b.row.StatusChangedAt {
			return a.row.StatusChangedAt > b.row.StatusChangedAt
		}
		return a.row.ID > b.row.ID
	default:
		if a.row.Created != b.row.Created {
			return a.row.Created > b.row.Created
		}
		return a.row.ID > b.row.ID
	}
}

func statusSet(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boardURL(name string) string {
	return companyOrigin + "/admin/board/" + slug(name)
}

func postURL(boardName, title string) string {
	return boardURL(boardName) + "/p/" + slug(title)
}

func userURL(name string) string {
	return companyOrigin + "/admin/users/" + slug(name)
}

func slug(value string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func publicError(err error) string {
	for err != nil {
		switch unwrapped := err.(type) {
		case interface{ Unwrap() []error }:
			next := unwrapped.Unwrap()
			if len(next) == 0 {
				return err.Error()
			}
			err = next[0]
		case interface{ Unwrap() error }:
			next := unwrapped.Unwrap()
			if next == nil {
				return err.Error()
			}
			err = next
		default:
			return err.Error()
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(generated.Error{Error: message})
}

func boardListError(message string) generated.CannyBoardsList400JSONResponse {
	return generated.CannyBoardsList400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func boardRetrieveError(message string) generated.CannyBoardsRetrieve400JSONResponse {
	return generated.CannyBoardsRetrieve400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func postListError(message string) generated.CannyPostsList400JSONResponse {
	return generated.CannyPostsList400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func postRetrieveError(message string) generated.CannyPostsRetrieve400JSONResponse {
	return generated.CannyPostsRetrieve400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func postCreateError(message string) generated.CannyPostsCreate400JSONResponse {
	return generated.CannyPostsCreate400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func postStatusError(message string) generated.CannyPostsChangeStatus400JSONResponse {
	return generated.CannyPostsChangeStatus400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func userRetrieveError(message string) generated.CannyUsersRetrieve400JSONResponse {
	return generated.CannyUsersRetrieve400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func userWriteError(message string) generated.CannyUsersCreateOrUpdate400JSONResponse {
	return generated.CannyUsersCreateOrUpdate400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func voteCreateError(message string) generated.CannyVotesCreate400JSONResponse {
	return generated.CannyVotesCreate400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}

func voteRetrieveError(message string) generated.CannyVotesRetrieve400JSONResponse {
	return generated.CannyVotesRetrieve400JSONResponse{ErrorJSONResponse: generated.ErrorJSONResponse{Error: message}}
}
