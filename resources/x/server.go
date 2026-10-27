package x

import (
	"context"
	"encoding/json"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/internal/socialstate"
	"github.com/dumbmachine/fabricate/resources/x/generated"
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

func (s *server) CreatePost(ctx context.Context, r generated.CreatePostRequestObject) (generated.CreatePostResponseObject, error) {
	if r.Body == nil || strings.TrimSpace(r.Body.Text) == "" || utf8.RuneCountInString(r.Body.Text) > 280 {
		return socialstate.Convert[generated.CreatePostdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Text must be 1 to 280 characters"}, "StatusCode": 400}), nil
	}
	id, err := s.ID(ctx)
	if err != nil {
		return nil, err
	}
	v := map[string]string{"id": id, "text": r.Body.Text}
	err = s.Put(ctx, "post", id, v)
	return socialstate.Convert[generated.CreatePost201JSONResponse](map[string]any{"data": v}), err
}
func (s *server) GetPost(ctx context.Context, r generated.GetPostRequestObject) (generated.GetPostResponseObject, error) {
	v, err := s.Get(ctx, "post", r.Id)
	if err != nil {
		return socialstate.Convert[generated.GetPostdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Post not found"}, "StatusCode": 404}), nil
	}
	return socialstate.Convert[generated.GetPost200JSONResponse](map[string]any{"data": v}), nil
}
func (s *server) GetMe(ctx context.Context, r generated.GetMeRequestObject) (generated.GetMeResponseObject, error) {
	a, err := s.Account(ctx)
	return socialstate.Convert[generated.GetMe200JSONResponse](map[string]any{"data": a}), err
}
