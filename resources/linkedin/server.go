package linkedin

import (
	"context"
	"encoding/json"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/internal/socialstate"
	"github.com/dumbmachine/fabricate/resources/linkedin/generated"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

type server struct{ *socialstate.Server }

func newServer(d httpresource.ServerDependencies) (*server, error) {
	base, err := socialstate.New(d)
	if err != nil {
		return nil, err
	}
	s := &server{base}
	s.Routes = generated.Handler(generated.NewStrictHandler(s, nil))
	return s, nil
}

var _ = json.Marshal
var _ = strings.TrimSpace
var _ = utf8.RuneCountInString
var _ = url.Parse
var _ = http.StatusOK

type created struct{ ID string }

func (c created) VisitCreatePostResponse(w http.ResponseWriter) error {
	w.Header().Set("x-restli-id", c.ID)
	w.WriteHeader(201)
	return nil
}
func (s *server) CreatePost(ctx context.Context, r generated.CreatePostRequestObject) (generated.CreatePostResponseObject, error) {
	a, err := s.Account(ctx)
	if err != nil {
		return nil, err
	}
	if r.Body == nil || r.Body.Author != a["id"] || strings.TrimSpace(r.Body.Commentary) == "" || utf8.RuneCountInString(r.Body.Commentary) > 3000 || !r.Body.Visibility.Valid() || !r.Body.LifecycleState.Valid() || !r.Body.Distribution.FeedDistribution.Valid() {
		return socialstate.Convert[generated.CreatePostdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Invalid post or author"}, "StatusCode": 400}), nil
	}
	id, err := s.ID(ctx)
	if err != nil {
		return nil, err
	}
	id = "urn:li:share:" + id
	err = s.Put(ctx, "post", id, map[string]any{"id": id, "author": r.Body.Author, "commentary": r.Body.Commentary, "lifecycleState": "PUBLISHED"})
	return created{id}, err
}
func (s *server) GetPost(ctx context.Context, r generated.GetPostRequestObject) (generated.GetPostResponseObject, error) {
	v, err := s.Get(ctx, "post", r.PostId)
	if err != nil {
		return socialstate.Convert[generated.GetPostdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Post not found"}, "StatusCode": 404}), nil
	}
	return socialstate.Convert[generated.GetPost200JSONResponse](v), nil
}
func (s *server) FindPosts(ctx context.Context, r generated.FindPostsRequestObject) (generated.FindPostsResponseObject, error) {
	a, err := s.Account(ctx)
	if err != nil {
		return nil, err
	}
	if r.Params.Q != "author" || r.Params.Author != a["id"] {
		return socialstate.Convert[generated.FindPostsdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Invalid author finder"}, "StatusCode": 400}), nil
	}
	v, err := s.List(ctx, "post")
	return socialstate.Convert[generated.FindPosts200JSONResponse](map[string]any{"elements": v}), err
}
