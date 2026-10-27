package instagram

import (
	"context"
	"encoding/json"
	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/instagram/generated"
	"github.com/dumbmachine/fabricate/resources/internal/socialstate"
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

func (s *server) CreateContainer(ctx context.Context, r generated.CreateContainerRequestObject) (generated.CreateContainerResponseObject, error) {
	a, err := s.Account(ctx)
	if err != nil {
		return nil, err
	}
	valid := r.Body != nil
	if valid {
		u, e := url.Parse(r.Body.ImageUrl)
		valid = e == nil && u.Scheme == "https" && u.Host != "" && strings.TrimSpace(r.Body.Caption) != "" && utf8.RuneCountInString(r.Body.Caption) <= 2200
	}
	if !valid || r.UserId != a["id"] {
		return socialstate.Convert[generated.CreateContainerdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Valid account, HTTPS image URL and caption required"}, "StatusCode": 400}), nil
	}
	id, err := s.ID(ctx)
	if err != nil {
		return nil, err
	}
	err = s.Put(ctx, "container", id, map[string]string{"id": id, "caption": r.Body.Caption, "media_url": r.Body.ImageUrl, "user_id": r.UserId})
	return generated.CreateContainer200JSONResponse{Id: id}, err
}
func (s *server) PublishContainer(ctx context.Context, r generated.PublishContainerRequestObject) (generated.PublishContainerResponseObject, error) {
	a, err := s.Account(ctx)
	if err != nil {
		return nil, err
	}
	if r.Body == nil || r.UserId != a["id"] {
		return socialstate.Convert[generated.PublishContainerdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Invalid account/container"}, "StatusCode": 400}), nil
	}
	c, err := s.Get(ctx, "container", r.Body.CreationId)
	if err != nil || c["user_id"] != r.UserId {
		return socialstate.Convert[generated.PublishContainerdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Container not found"}, "StatusCode": 404}), nil
	}
	id := r.Body.CreationId
	v := map[string]any{"id": id, "caption": c["caption"], "media_url": c["media_url"], "media_type": "IMAGE", "permalink": "https://www.instagram.com/p/" + id + "/"}
	if _, err = s.Get(ctx, "media", id); err == nil {
		return socialstate.Convert[generated.PublishContainerdefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Container already published"}, "StatusCode": 400}), nil
	}
	err = s.Put(ctx, "media", id, v)
	return generated.PublishContainer200JSONResponse{Id: id}, err
}
func (s *server) GetMedia(ctx context.Context, r generated.GetMediaRequestObject) (generated.GetMediaResponseObject, error) {
	v, err := s.Get(ctx, "media", r.MediaId)
	if err != nil {
		return socialstate.Convert[generated.GetMediadefaultJSONResponse](map[string]any{"Body": map[string]string{"message": "Media not found"}, "StatusCode": 404}), nil
	}
	return socialstate.Convert[generated.GetMedia200JSONResponse](v), nil
}
func (s *server) GetUser(ctx context.Context, r generated.GetUserRequestObject) (generated.GetUserResponseObject, error) {
	a, err := s.Account(ctx)
	if err != nil {
		return nil, err
	}

	return socialstate.Convert[generated.GetUser200JSONResponse](a), nil
}
