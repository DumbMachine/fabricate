package greenhouse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dumbmachine/fabricate/httpresource"
	"github.com/dumbmachine/fabricate/resources/greenhouse/generated"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type server struct {
	db      *sql.DB
	clock   httpresource.Clock
	ids     httpresource.IDGenerator
	handler http.Handler
}

var _ generated.StrictServerInterface = (*server)(nil)

func newServer(ctx context.Context, dependencies httpresource.ServerDependencies) (*server, error) {
	if dependencies.DB == nil || dependencies.Clock == nil || dependencies.IDs == nil || dependencies.Secrets == nil {
		return nil, fmt.Errorf("greenhouse: database, clock, ID generator, and secrets are required")
	}
	token, err := dependencies.Secrets.Get(ctx, "token")
	if err != nil {
		return nil, fmt.Errorf("greenhouse: load synthetic token: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("greenhouse: synthetic token is empty")
	}
	spec, err := generated.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("greenhouse: load OpenAPI: %w", err)
	}
	impl := &server{db: dependencies.DB, clock: dependencies.Clock, ids: dependencies.IDs}
	strict := generated.NewStrictHandler(impl, nil)
	generatedHandler := generated.Handler(strict)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		DoNotValidateServers: true,
		Options: openapi3filter.Options{AuthenticationFunc: func(_ context.Context, input *openapi3filter.AuthenticationInput) error {
			header := input.RequestValidationInput.Request.Header.Get("Authorization")
			if header != "Bearer "+token {
				return input.NewError(errors.New("invalid synthetic bearer token"))
			}
			return nil
		}},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
			status := opts.StatusCode
			if status == 0 {
				status = http.StatusBadRequest
			}
			writeError(w, status, err.Error())
		},
	})
	inner := validator(generatedHandler)
	impl.handler = http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if len(request.URL.Path) > 1 && strings.HasSuffix(request.URL.Path, "/") {
			cloned := request.Clone(request.Context())
			cloned.URL.Path = strings.TrimRight(cloned.URL.Path, "/")
			cloned.URL.RawPath = ""
			inner.ServeHTTP(w, cloned)
			return
		}
		inner.ServeHTTP(w, request)
	})
	return impl, nil
}

func (s *server) Handler() http.Handler       { return s.handler }
func (s *server) Close(context.Context) error { return nil }

func (s *server) ListJobs(ctx context.Context, request generated.ListJobsRequestObject) (generated.ListJobsResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	jobs := pageOf(w.Jobs, request.Params.Page, request.Params.PerPage)
	out := make([]generated.Job, 0, len(jobs))
	for _, job := range jobs {
		mapped, err := w.jobAPI(job)
		if err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return generated.ListJobs200JSONResponse(out), nil
}

func (s *server) GetJob(ctx context.Context, request generated.GetJobRequestObject) (generated.GetJobResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	job, ok := w.jobs[request.Id]
	if !ok {
		return generated.GetJobdefaultJSONResponse{Body: errorBody("job not found"), StatusCode: http.StatusNotFound}, nil
	}
	mapped, err := w.jobAPI(job)
	if err != nil {
		return nil, err
	}
	return generated.GetJob200JSONResponse(mapped), nil
}

func (s *server) ListJobStages(ctx context.Context, request generated.ListJobStagesRequestObject) (generated.ListJobStagesResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := w.jobs[request.Id]; !ok {
		return generated.ListJobStagesdefaultJSONResponse{Body: errorBody("job not found"), StatusCode: http.StatusNotFound}, nil
	}
	stages := pageOf(w.stagesFor(request.Id), request.Params.Page, request.Params.PerPage)
	out := make([]generated.JobStage, 0, len(stages))
	for _, stage := range stages {
		out = append(out, stageAPI(stage))
	}
	return generated.ListJobStages200JSONResponse(out), nil
}

func (s *server) ListCandidates(ctx context.Context, request generated.ListCandidatesRequestObject) (generated.ListCandidatesResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	matched := pageOf(w.filterCandidates(strPtr(request.Params.Email), strPtr(request.Params.JobId)), request.Params.Page, request.Params.PerPage)
	out := make([]generated.Candidate, 0, len(matched))
	for _, candidate := range matched {
		mapped, err := w.candidateAPI(candidate)
		if err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return generated.ListCandidates200JSONResponse(out), nil
}

func (s *server) GetCandidate(ctx context.Context, request generated.GetCandidateRequestObject) (generated.GetCandidateResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	candidate, ok := w.candidates[request.Id]
	if !ok {
		return generated.GetCandidatedefaultJSONResponse{Body: errorBody("candidate not found"), StatusCode: http.StatusNotFound}, nil
	}
	mapped, err := w.candidateAPI(candidate)
	if err != nil {
		return nil, err
	}
	return generated.GetCandidate200JSONResponse(mapped), nil
}

func (s *server) GetActivityFeed(ctx context.Context, request generated.GetActivityFeedRequestObject) (generated.GetActivityFeedResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := w.candidates[request.Id]; !ok {
		return generated.GetActivityFeeddefaultJSONResponse{Body: errorBody("candidate not found"), StatusCode: http.StatusNotFound}, nil
	}
	notes, err := w.notesAPI(request.Id)
	if err != nil {
		return nil, err
	}
	return generated.GetActivityFeed200JSONResponse{
		Notes: notes, Emails: []generated.EmailActivity{}, Activities: []generated.Activity{},
	}, nil
}

func (s *server) CreateCandidateNote(ctx context.Context, request generated.CreateCandidateNoteRequestObject) (generated.CreateCandidateNoteResponseObject, error) {
	if request.Body == nil {
		return generated.CreateCandidateNotedefaultJSONResponse{Body: errorBody("Request body is required"), StatusCode: http.StatusBadRequest}, nil
	}
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := w.candidates[request.Id]; !ok {
		return generated.CreateCandidateNotedefaultJSONResponse{Body: errorBody("candidate not found"), StatusCode: http.StatusNotFound}, nil
	}
	author, ok := w.users[request.Body.UserId]
	if !ok {
		return generated.CreateCandidateNotedefaultJSONResponse{Body: fieldError("user_id", "user not found"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	if _, ok := w.users[request.Params.OnBehalfOf]; !ok {
		return generated.CreateCandidateNotedefaultJSONResponse{Body: fieldError("On-Behalf-Of", "user not found"), StatusCode: http.StatusUnprocessableEntity}, nil
	}
	id, err := s.ids.Next(ctx, "greenhouse.note")
	if err != nil {
		return nil, fmt.Errorf("greenhouse: allocate note ID: %w", err)
	}
	note := fixtureNote{
		ID: id, CandidateID: request.Id, UserID: author.ID, Body: request.Body.Body,
		Visibility: string(request.Body.Visibility), CreatedAt: s.clock.Now().UTC().Format(time.RFC3339),
	}
	if err := persistNote(ctx, s.db, note); err != nil {
		return nil, err
	}
	return generated.CreateCandidateNote201JSONResponse(noteAPI(note, author)), nil
}

func (s *server) ListApplications(ctx context.Context, request generated.ListApplicationsRequestObject) (generated.ListApplicationsResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	matched := pageOf(w.filterApplications(strPtr(request.Params.JobId), strPtr(request.Params.Status)), request.Params.Page, request.Params.PerPage)
	out, err := w.applicationsAPI(matched)
	if err != nil {
		return nil, err
	}
	return generated.ListApplications200JSONResponse(out), nil
}

func (s *server) GetApplication(ctx context.Context, request generated.GetApplicationRequestObject) (generated.GetApplicationResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	app, ok := w.applications[request.Id]
	if !ok {
		return generated.GetApplicationdefaultJSONResponse{Body: errorBody("application not found"), StatusCode: http.StatusNotFound}, nil
	}
	mapped, err := w.applicationAPI(app)
	if err != nil {
		return nil, err
	}
	return generated.GetApplication200JSONResponse(mapped), nil
}

func (s *server) ListApplicationOffers(ctx context.Context, request generated.ListApplicationOffersRequestObject) (generated.ListApplicationOffersResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := w.applications[request.Id]; !ok {
		return generated.ListApplicationOffersdefaultJSONResponse{Body: errorBody("application not found"), StatusCode: http.StatusNotFound}, nil
	}
	offers := pageOf(w.offersFor(request.Id), request.Params.Page, request.Params.PerPage)
	out := make([]generated.Offer, 0, len(offers))
	for _, offer := range offers {
		out = append(out, offerAPI(offer))
	}
	return generated.ListApplicationOffers200JSONResponse(out), nil
}

func (s *server) ListApplicationScorecards(ctx context.Context, request generated.ListApplicationScorecardsRequestObject) (generated.ListApplicationScorecardsResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := w.applications[request.Id]; !ok {
		return generated.ListApplicationScorecardsdefaultJSONResponse{Body: errorBody("application not found"), StatusCode: http.StatusNotFound}, nil
	}
	cards := pageOf(w.scorecardsFor(request.Id), request.Params.Page, request.Params.PerPage)
	out, err := w.scorecardsAPI(cards)
	if err != nil {
		return nil, err
	}
	return generated.ListApplicationScorecards200JSONResponse(out), nil
}

func (s *server) ListOffers(ctx context.Context, request generated.ListOffersRequestObject) (generated.ListOffersResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	matched := []fixtureOffer{}
	status := strPtr(request.Params.Status)
	for _, offer := range w.Offers {
		if status != "" && offer.Status != status {
			continue
		}
		matched = append(matched, offer)
	}
	offers := pageOf(matched, request.Params.Page, request.Params.PerPage)
	out := make([]generated.Offer, 0, len(offers))
	for _, offer := range offers {
		out = append(out, offerAPI(offer))
	}
	return generated.ListOffers200JSONResponse(out), nil
}

func (s *server) GetOffer(ctx context.Context, request generated.GetOfferRequestObject) (generated.GetOfferResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	for _, offer := range w.Offers {
		if offer.ID == request.Id {
			return generated.GetOffer200JSONResponse(offerAPI(offer)), nil
		}
	}
	return generated.GetOfferdefaultJSONResponse{Body: errorBody("offer not found"), StatusCode: http.StatusNotFound}, nil
}

func (s *server) ListScorecards(ctx context.Context, request generated.ListScorecardsRequestObject) (generated.ListScorecardsResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	cards := pageOf(w.Scorecards, request.Params.Page, request.Params.PerPage)
	out, err := w.scorecardsAPI(cards)
	if err != nil {
		return nil, err
	}
	return generated.ListScorecards200JSONResponse(out), nil
}

func (s *server) ListUsers(ctx context.Context, request generated.ListUsersRequestObject) (generated.ListUsersResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	users := pageOf(w.filterUsers(strPtr(request.Params.Email)), request.Params.Page, request.Params.PerPage)
	out := make([]generated.User, 0, len(users))
	for _, user := range users {
		out = append(out, userAPI(user))
	}
	return generated.ListUsers200JSONResponse(out), nil
}

func (s *server) GetUser(ctx context.Context, request generated.GetUserRequestObject) (generated.GetUserResponseObject, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, err
	}
	user, ok := w.users[request.Id]
	if !ok {
		return generated.GetUserdefaultJSONResponse{Body: errorBody("user not found"), StatusCode: http.StatusNotFound}, nil
	}
	return generated.GetUser200JSONResponse(userAPI(user)), nil
}

func (s *server) world(ctx context.Context) (world, error) {
	state, err := readState(ctx, s.db)
	if err != nil {
		return world{}, err
	}
	return organize(state), nil
}

func persistNote(ctx context.Context, db *sql.DB, note fixtureNote) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("greenhouse: begin note: %w", err)
	}
	defer tx.Rollback()
	var candidate fixtureCandidate
	if err := loadDocument(ctx, tx, collectionCandidates, note.CandidateID, &candidate); err != nil {
		return fmt.Errorf("greenhouse: load candidate %s: %w", note.CandidateID, err)
	}
	if err := insertDocument(ctx, tx, collectionNotes, note.ID, note); err != nil {
		return err
	}
	candidate.LastActivity = note.CreatedAt
	candidate.UpdatedAt = note.CreatedAt
	if err := saveDocument(ctx, tx, collectionCandidates, candidate.ID, candidate); err != nil {
		return err
	}
	apps, err := readCollection[fixtureApplication](ctx, tx, collectionApplications)
	if err != nil {
		return err
	}
	for _, app := range apps {
		if app.CandidateID != note.CandidateID {
			continue
		}
		app.LastActivityAt = note.CreatedAt
		if err := saveDocument(ctx, tx, collectionApplications, app.ID, app); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("greenhouse: commit note: %w", err)
	}
	return nil
}

type world struct {
	fixtureState
	users        map[string]fixtureUser
	jobs         map[string]fixtureJob
	stages       map[string]fixtureJobStage
	departments  map[string]fixtureDepartment
	offices      map[string]fixtureOffice
	candidates   map[string]fixtureCandidate
	applications map[string]fixtureApplication
}

func organize(state fixtureState) world {
	w := world{
		fixtureState: state,
		users:        map[string]fixtureUser{},
		jobs:         map[string]fixtureJob{},
		stages:       map[string]fixtureJobStage{},
		departments:  map[string]fixtureDepartment{},
		offices:      map[string]fixtureOffice{},
		candidates:   map[string]fixtureCandidate{},
		applications: map[string]fixtureApplication{},
	}
	for _, item := range state.Users {
		w.users[item.ID] = item
	}
	for _, item := range state.Jobs {
		w.jobs[item.ID] = item
	}
	for _, item := range state.JobStages {
		w.stages[item.ID] = item
	}
	for _, item := range state.Departments {
		w.departments[item.ID] = item
	}
	for _, item := range state.Offices {
		w.offices[item.ID] = item
	}
	for _, item := range state.Candidates {
		w.candidates[item.ID] = item
	}
	for _, item := range state.Applications {
		w.applications[item.ID] = item
	}
	return w
}

func (w world) filterCandidates(email, jobID string) []fixtureCandidate {
	out := []fixtureCandidate{}
	for _, candidate := range w.Candidates {
		if email != "" && !hasEmail(candidate, email) {
			continue
		}
		if jobID != "" && !w.candidateOnJob(candidate.ID, jobID) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func (w world) filterApplications(jobID, status string) []fixtureApplication {
	out := []fixtureApplication{}
	for _, app := range w.Applications {
		if jobID != "" && app.JobID != jobID {
			continue
		}
		if status != "" && app.Status != status {
			continue
		}
		out = append(out, app)
	}
	return out
}

func (w world) filterUsers(email string) []fixtureUser {
	if email == "" {
		if w.Users == nil {
			return []fixtureUser{}
		}
		return w.Users
	}
	out := []fixtureUser{}
	for _, user := range w.Users {
		if strings.EqualFold(user.Email, email) {
			out = append(out, user)
			continue
		}
		for _, extra := range user.Emails {
			if strings.EqualFold(extra, email) {
				out = append(out, user)
				break
			}
		}
	}
	return out
}

func (w world) candidateOnJob(candidateID, jobID string) bool {
	for _, app := range w.Applications {
		if app.CandidateID == candidateID && app.JobID == jobID {
			return true
		}
	}
	return false
}

func (w world) stagesFor(jobID string) []fixtureJobStage {
	out := []fixtureJobStage{}
	for _, stage := range w.JobStages {
		if stage.JobID == jobID {
			out = append(out, stage)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (w world) applicationsFor(candidateID string) []fixtureApplication {
	out := []fixtureApplication{}
	for _, app := range w.Applications {
		if app.CandidateID == candidateID {
			out = append(out, app)
		}
	}
	return out
}

func (w world) offersFor(applicationID string) []fixtureOffer {
	out := []fixtureOffer{}
	for _, offer := range w.Offers {
		if offer.ApplicationID == applicationID {
			out = append(out, offer)
		}
	}
	return out
}

func (w world) scorecardsFor(applicationID string) []fixtureScorecard {
	out := []fixtureScorecard{}
	for _, card := range w.Scorecards {
		if card.ApplicationID == applicationID {
			out = append(out, card)
		}
	}
	return out
}

func (w world) notesFor(candidateID string) []fixtureNote {
	out := []fixtureNote{}
	for _, note := range w.Notes {
		if note.CandidateID == candidateID {
			out = append(out, note)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (w world) jobAPI(job fixtureJob) (generated.Job, error) {
	department, ok := w.departments[job.DepartmentID]
	if !ok {
		return generated.Job{}, fmt.Errorf("greenhouse: job %s department %s not found", job.ID, job.DepartmentID)
	}
	office, ok := w.offices[job.OfficeID]
	if !ok {
		return generated.Job{}, fmt.Errorf("greenhouse: job %s office %s not found", job.ID, job.OfficeID)
	}
	managers, err := w.userRefs(job.HiringManagers)
	if err != nil {
		return generated.Job{}, err
	}
	recruiters, err := w.hiringMembers(job.Recruiters, job.ResponsibleRecruiterID)
	if err != nil {
		return generated.Job{}, err
	}
	coordinators, err := w.hiringMembers(job.Coordinators, job.ResponsibleRecruiterID)
	if err != nil {
		return generated.Job{}, err
	}
	sourcers, err := w.userRefs(job.Sourcers)
	if err != nil {
		return generated.Job{}, err
	}
	return generated.Job{
		Id: job.ID, Name: job.Name, RequisitionId: job.RequisitionID, Notes: job.Notes,
		Confidential: job.Confidential, Status: generated.JobStatus(job.Status),
		CreatedAt: job.CreatedAt, OpenedAt: job.OpenedAt, ClosedAt: job.ClosedAt, UpdatedAt: job.UpdatedAt,
		Departments: []generated.DepartmentRef{{Id: department.ID, Name: department.Name}},
		Offices:     []generated.OfficeRef{{Id: office.ID, Name: office.Name, Location: generated.Location{Name: office.LocationName}}},
		HiringTeam: generated.HiringTeam{
			HiringManagers: managers, Recruiters: recruiters, Coordinators: coordinators, Sourcers: sourcers,
		},
	}, nil
}

func (w world) candidateAPI(candidate fixtureCandidate) (generated.Candidate, error) {
	recruiter, err := w.userBy(candidate.RecruiterID)
	if err != nil {
		return generated.Candidate{}, err
	}
	apps, err := w.applicationsAPI(w.applicationsFor(candidate.ID))
	if err != nil {
		return generated.Candidate{}, err
	}
	ids := make([]string, 0, len(apps))
	for _, app := range apps {
		ids = append(ids, app.Id)
	}
	return generated.Candidate{
		Id: candidate.ID, FirstName: candidate.FirstName, LastName: candidate.LastName,
		Company: candidate.Company, Title: candidate.Title, CreatedAt: candidate.CreatedAt,
		UpdatedAt: candidate.UpdatedAt, LastActivity: candidate.LastActivity, IsPrivate: candidate.IsPrivate,
		ApplicationIds: ids, PhoneNumbers: phonesAPI(candidate.Phones), EmailAddresses: emailsAPI(candidate.Emails),
		Recruiter: userRef(recruiter), CanEmail: candidate.CanEmail, Tags: stringsOrEmpty(candidate.Tags),
		Applications: apps, Attachments: attachmentsAPI(candidate.Attachments),
	}, nil
}

func (w world) applicationsAPI(apps []fixtureApplication) ([]generated.Application, error) {
	out := make([]generated.Application, 0, len(apps))
	for _, app := range apps {
		mapped, err := w.applicationAPI(app)
		if err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return out, nil
}

func (w world) applicationAPI(app fixtureApplication) (generated.Application, error) {
	recruiter, err := w.userBy(app.RecruiterID)
	if err != nil {
		return generated.Application{}, err
	}
	job, ok := w.jobs[app.JobID]
	if !ok {
		return generated.Application{}, fmt.Errorf("greenhouse: application %s job %s not found", app.ID, app.JobID)
	}
	stage, ok := w.stages[app.StageID]
	if !ok {
		return generated.Application{}, fmt.Errorf("greenhouse: application %s stage %s not found", app.ID, app.StageID)
	}
	candidate, ok := w.candidates[app.CandidateID]
	if !ok {
		return generated.Application{}, fmt.Errorf("greenhouse: application %s candidate %s not found", app.ID, app.CandidateID)
	}
	return generated.Application{
		Id: app.ID, CandidateId: app.CandidateID, Prospect: app.Prospect, AppliedAt: app.AppliedAt,
		RejectedAt: app.RejectedAt, LastActivityAt: app.LastActivityAt,
		Source:    generated.Source{Id: app.SourceID, PublicName: app.SourceName},
		Recruiter: userRef(recruiter), Jobs: []generated.JobRef{{Id: job.ID, Name: job.Name}},
		Status: generated.ApplicationStatus(app.Status), CurrentStage: generated.StageRef{Id: stage.ID, Name: stage.Name},
		Attachments: attachmentsAPI(candidate.Attachments),
	}, nil
}

func (w world) notesAPI(candidateID string) ([]generated.Note, error) {
	notes := w.notesFor(candidateID)
	out := make([]generated.Note, 0, len(notes))
	for _, note := range notes {
		author, err := w.userBy(note.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, noteAPI(note, author))
	}
	return out, nil
}

func (w world) scorecardsAPI(cards []fixtureScorecard) ([]generated.Scorecard, error) {
	out := make([]generated.Scorecard, 0, len(cards))
	for _, card := range cards {
		mapped, err := w.scorecardAPI(card)
		if err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return out, nil
}

func (w world) scorecardAPI(card fixtureScorecard) (generated.Scorecard, error) {
	submitter, err := w.userBy(card.SubmittedBy)
	if err != nil {
		return generated.Scorecard{}, err
	}
	interviewer, err := w.userBy(card.InterviewerID)
	if err != nil {
		return generated.Scorecard{}, err
	}
	attrs := make([]generated.ScorecardAttribute, 0, len(card.Attributes))
	ratings := generated.ScorecardRatings{
		DefinitelyNot: []string{}, No: []string{}, Mixed: []string{}, Yes: []string{}, StrongYes: []string{},
	}
	for _, attr := range card.Attributes {
		attrs = append(attrs, generated.ScorecardAttribute{
			Name: attr.Name, Type: attr.Type, Note: attr.Note, Rating: generated.AttributeRating(attr.Rating),
		})
		switch attr.Rating {
		case "definitely_not":
			ratings.DefinitelyNot = append(ratings.DefinitelyNot, attr.Name)
		case "no":
			ratings.No = append(ratings.No, attr.Name)
		case "mixed":
			ratings.Mixed = append(ratings.Mixed, attr.Name)
		case "yes":
			ratings.Yes = append(ratings.Yes, attr.Name)
		case "strong_yes":
			ratings.StrongYes = append(ratings.StrongYes, attr.Name)
		}
	}
	questions := make([]generated.ScorecardQuestion, 0, len(card.Questions))
	for _, question := range card.Questions {
		questions = append(questions, generated.ScorecardQuestion{Id: question.ID, Question: question.Question, Answer: question.Answer})
	}
	return generated.Scorecard{
		Id: card.ID, UpdatedAt: card.UpdatedAt, CreatedAt: card.CreatedAt, Interview: card.Interview,
		InterviewStep: generated.InterviewStep{Id: card.InterviewStepID, Name: card.InterviewStepName},
		CandidateId:   card.CandidateID, ApplicationId: card.ApplicationID, InterviewedAt: card.InterviewedAt,
		SubmittedBy: userRef(submitter), Interviewer: userRef(interviewer), SubmittedAt: card.SubmittedAt,
		OverallRecommendation: generated.Recommendation(card.OverallRecommendation),
		Attributes:            attrs, Ratings: ratings, Questions: questions,
	}, nil
}

func (w world) userBy(id string) (fixtureUser, error) {
	user, ok := w.users[id]
	if !ok {
		return fixtureUser{}, fmt.Errorf("greenhouse: user %s not found", id)
	}
	return user, nil
}

func (w world) userRefs(ids []string) ([]generated.UserRef, error) {
	out := make([]generated.UserRef, 0, len(ids))
	for _, id := range ids {
		user, err := w.userBy(id)
		if err != nil {
			return nil, err
		}
		out = append(out, userRef(user))
	}
	return out, nil
}

func (w world) hiringMembers(ids []string, responsibleID string) ([]generated.HiringTeamMember, error) {
	out := make([]generated.HiringTeamMember, 0, len(ids))
	for _, id := range ids {
		user, err := w.userBy(id)
		if err != nil {
			return nil, err
		}
		out = append(out, generated.HiringTeamMember{
			Id: user.ID, FirstName: user.FirstName, LastName: user.LastName, Name: user.Name,
			EmployeeId: user.EmployeeID, Responsible: id == responsibleID,
		})
	}
	return out, nil
}

func stageAPI(stage fixtureJobStage) generated.JobStage {
	return generated.JobStage{
		Id: stage.ID, Name: stage.Name, CreatedAt: stage.CreatedAt, UpdatedAt: stage.UpdatedAt,
		Active: stage.Active, JobId: stage.JobID, Priority: stage.Priority, Interviews: []generated.Interview{},
	}
}

func offerAPI(offer fixtureOffer) generated.Offer {
	custom := map[string]string{}
	keyed := map[string]generated.KeyedCustomField{}
	for key, value := range offer.CustomFields {
		custom[key] = value
		keyed[key] = generated.KeyedCustomField{Name: key, Type: "short_text", Value: value}
	}
	return generated.Offer{
		Id: offer.ID, Version: offer.Version, ApplicationId: offer.ApplicationID, JobId: offer.JobID,
		CandidateId: offer.CandidateID, CreatedAt: offer.CreatedAt, UpdatedAt: offer.UpdatedAt,
		SentAt: offer.SentAt, ResolvedAt: offer.ResolvedAt, StartsAt: offer.StartsAt,
		Status: generated.OfferStatus(offer.Status), CustomFields: custom, KeyedCustomFields: keyed,
	}
}

func userAPI(user fixtureUser) generated.User {
	return generated.User{
		Id: user.ID, Name: user.Name, FirstName: user.FirstName, LastName: user.LastName,
		PrimaryEmailAddress: openapi_types.Email(user.Email), UpdatedAt: user.UpdatedAt, CreatedAt: user.CreatedAt,
		Disabled: user.Disabled, SiteAdmin: user.SiteAdmin, Emails: stringsOrEmpty(user.Emails), EmployeeId: user.EmployeeID,
	}
}

func noteAPI(note fixtureNote, author fixtureUser) generated.Note {
	visibility := generated.NoteVisibility(note.Visibility)
	return generated.Note{
		Id: note.ID, CreatedAt: note.CreatedAt, Body: note.Body, User: userRef(author),
		Private: visibility == generated.Private, Visiblity: visibility, Visibility: visibility,
	}
}

func userRef(user fixtureUser) generated.UserRef {
	return generated.UserRef{
		Id: user.ID, FirstName: user.FirstName, LastName: user.LastName, Name: user.Name, EmployeeId: user.EmployeeID,
	}
}

func emailsAPI(contacts []fixtureContact) []generated.EmailAddress {
	out := make([]generated.EmailAddress, 0, len(contacts))
	for _, contact := range contacts {
		out = append(out, generated.EmailAddress{Value: contact.Value, Type: generated.EmailType(contact.Type)})
	}
	return out
}

func phonesAPI(contacts []fixtureContact) []generated.PhoneNumber {
	out := make([]generated.PhoneNumber, 0, len(contacts))
	for _, contact := range contacts {
		out = append(out, generated.PhoneNumber{Value: contact.Value, Type: generated.PhoneType(contact.Type)})
	}
	return out
}

func attachmentsAPI(items []fixtureAttachment) []generated.Attachment {
	out := make([]generated.Attachment, 0, len(items))
	for _, item := range items {
		out = append(out, generated.Attachment{
			Filename: item.Filename, Url: item.URL, Type: generated.AttachmentType(item.Type), CreatedAt: item.CreatedAt,
		})
	}
	return out
}

func pageOf[T any](items []T, page, perPage *int) []T {
	p := 1
	if page != nil {
		p = *page
	}
	size := 100
	if perPage != nil {
		size = *perPage
	}
	if len(items) == 0 || p < 1 || size < 1 {
		return []T{}
	}
	start := (p - 1) * size
	if start >= len(items) {
		return []T{}
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func strPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringsOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func hasEmail(candidate fixtureCandidate, email string) bool {
	for _, item := range candidate.Emails {
		if strings.EqualFold(item.Value, email) {
			return true
		}
	}
	return false
}

func errorBody(message string) generated.Error {
	return generated.Error{Message: message}
}

func fieldError(field, message string) generated.Error {
	return generated.Error{Message: "Validation error", Errors: &[]generated.FieldError{{Field: field, Message: message}}}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody(message))
}
