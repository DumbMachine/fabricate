package jsm

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"time"

	"github.com/dumbmachine/fabricate/resources/jsm/generated"
)

type pageWindow struct {
	start, limit int
	last         bool
}

func cutPage[T any](items []T, start, limit *int) ([]T, pageWindow, error) {
	window := pageWindow{start: 0, limit: 50, last: true}
	if start != nil {
		window.start = *start
	}
	if limit != nil {
		window.limit = *limit
	}
	if window.start < 0 || window.limit < 1 || window.limit > 100 {
		return nil, window, fmt.Errorf("invalid start or limit")
	}
	if items == nil {
		items = []T{}
	}
	if window.start > len(items) {
		return nil, window, fmt.Errorf("start is past the end of the collection")
	}
	end := window.start + window.limit
	if end >= len(items) {
		end = len(items)
		window.last = true
	} else {
		window.last = false
	}
	return items[window.start:end], window, nil
}

func pagedLinks(path string, window pageWindow, pageSize, total int) generated.PagedLinkDTO {
	links := generated.PagedLinkDTO{
		Base: apiBase, Context: "",
		Self: fmt.Sprintf("%s%s?start=%d&limit=%d", apiBase, path, window.start, window.limit),
	}
	if window.start+pageSize < total {
		next := fmt.Sprintf("%s%s?start=%d&limit=%d", apiBase, path, window.start+pageSize, window.limit)
		links.Next = &next
	}
	if window.start > 0 {
		prevStart := window.start - window.limit
		if prevStart < 0 {
			prevStart = 0
		}
		prev := fmt.Sprintf("%s%s?start=%d&limit=%d", apiBase, path, prevStart, window.limit)
		links.Prev = &prev
	}
	return links
}

func (s *server) requestDTO(ctx context.Context, row requestRow, expand map[string]bool) (generated.CustomerRequestDTO, error) {
	reporter, err := s.userByID(ctx, row.ReporterAccountID)
	if err != nil {
		return generated.CustomerRequestDTO{}, err
	}
	created, err := dateDTO(row.CreatedAt)
	if err != nil {
		return generated.CustomerRequestDTO{}, err
	}
	statusAt, err := dateDTO(row.UpdatedAt)
	if err != nil {
		return generated.CustomerRequestDTO{}, err
	}
	category := generated.CustomerRequestStatusDTOStatusCategory(row.StatusCategory)
	dto := generated.CustomerRequestDTO{
		IssueId: row.IssueID, IssueKey: row.IssueKey, Summary: row.Summary,
		RequestTypeId: row.RequestTypeID, ServiceDeskId: row.ServiceDeskID,
		CreatedDate: created, Reporter: userDTO(reporter), RequestFieldValues: fieldValues(row.Summary, row.Description),
		CurrentStatus:     generated.CustomerRequestStatusDTO{Status: row.Status, StatusCategory: category, StatusDate: statusAt},
		UnderscoreExpands: requestExpandList(),
		UnderscoreLinks: generated.CustomerRequestLinkDTO{
			Self:     apiBase + "/request/" + url.PathEscape(row.IssueID),
			JiraRest: siteBase + "/rest/api/3/issue/" + url.PathEscape(row.IssueID),
			Web:      siteBase + "/servicedesk/customer/portal/" + url.PathEscape(row.ServiceDeskID) + "/" + url.PathEscape(row.IssueKey),
			Agent:    siteBase + "/browse/" + url.PathEscape(row.IssueKey),
		},
	}
	if expand["serviceDesk"] {
		desk, err := s.deskByID(ctx, row.ServiceDeskID)
		if err != nil {
			return generated.CustomerRequestDTO{}, err
		}
		copied := serviceDeskDTO(desk)
		dto.ServiceDesk = &copied
	}
	if expand["requestType"] {
		kind, err := s.requestTypeByID(ctx, row.ServiceDeskID, row.RequestTypeID)
		if err != nil {
			return generated.CustomerRequestDTO{}, err
		}
		copied := requestTypeDTO(kind)
		dto.RequestType = &copied
	}
	return dto, nil
}

func (s *server) commentDTO(ctx context.Context, request requestRow, comment commentRow, expand map[string]bool) (generated.CommentDTO, error) {
	author, err := s.userByID(ctx, comment.AuthorAccountID)
	if err != nil {
		return generated.CommentDTO{}, err
	}
	created, err := dateDTO(comment.CreatedAt)
	if err != nil {
		return generated.CommentDTO{}, err
	}
	dto := generated.CommentDTO{
		Id: comment.ID, Body: comment.Body, Public: comment.Public, Author: userDTO(author), Created: created,
		UnderscoreExpands: commentExpandList(),
		UnderscoreLinks:   generated.SelfLinkDTO{Self: apiBase + "/request/" + url.PathEscape(request.IssueID) + "/comment/" + url.PathEscape(comment.ID)},
	}
	if expand["renderedBody"] {
		dto.RenderedBody = renderedHTML(comment.Body)
	}
	return dto, nil
}

func serviceDeskDTO(desk deskRow) generated.ServiceDeskDTO {
	return generated.ServiceDeskDTO{
		Id: desk.ID, ProjectId: desk.ProjectID, ProjectKey: desk.ProjectKey, ProjectName: desk.ProjectName,
		ProjectTypeKey:  "service_desk",
		UnderscoreLinks: generated.SelfLinkDTO{Self: apiBase + "/servicedesk/" + url.PathEscape(desk.ID)},
	}
}

func requestTypeDTO(kind requestTypeRow) generated.RequestTypeDTO {
	return generated.RequestTypeDTO{
		Id: kind.ID, Name: kind.Name, Description: kind.Description, HelpText: kind.HelpText,
		IssueTypeId: kind.IssueTypeID, ServiceDeskId: kind.ServiceDeskID, PortalId: kind.ServiceDeskID,
		GroupIds: []string{requestTypeGroupID}, RestrictionStatus: generated.OPEN,
		UnderscoreExpands: []string{"field"},
		UnderscoreLinks:   generated.SelfLinkDTO{Self: apiBase + "/servicedesk/" + url.PathEscape(kind.ServiceDeskID) + "/requesttype/" + url.PathEscape(kind.ID)},
	}
}

func userDTO(user userRow) generated.UserDTO {
	name := user.AccountID
	link := siteBase + "/rest/api/3/user?accountId=" + url.QueryEscape(user.AccountID)
	return generated.UserDTO{
		AccountId: user.AccountID, DisplayName: user.DisplayName, Active: user.Active,
		EmailAddress: user.Email, TimeZone: user.TimeZone, Name: &name, Key: &name,
		UnderscoreLinks: generated.UserLinkDTO{JiraRest: link, Self: link},
	}
}

func fieldValues(summary, description string) []generated.CustomerRequestFieldValueDTO {
	values := []generated.CustomerRequestFieldValueDTO{{FieldId: "summary", Label: summaryLabel, Value: summary}}
	if description != "" {
		values = append(values, generated.CustomerRequestFieldValueDTO{
			FieldId: "description", Label: descriptionLabel, Value: description, RenderedValue: renderedHTML(description),
		})
	}
	return values
}

func renderedHTML(text string) *struct {
	Html string `json:"html"`
} {
	value := struct {
		Html string `json:"html"`
	}{Html: "<p>" + html.EscapeString(text) + "</p>"}
	return &value
}

func dateDTO(raw string) (generated.DateDTO, error) {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return generated.DateDTO{}, fmt.Errorf("jsm: parse time %q: %w", raw, err)
	}
	parsed = parsed.UTC()
	return generated.DateDTO{
		EpochMillis: parsed.UnixMilli(),
		Friendly:    parsed.Format("Monday 3:04 PM"),
		Iso8601:     parsed.Format("2006-01-02T15:04:05-0700"),
		Jira:        parsed.Format("2006-01-02T15:04:05.000-0700"),
	}, nil
}
