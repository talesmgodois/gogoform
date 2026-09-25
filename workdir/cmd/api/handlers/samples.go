package handlers

import "net/http"

// SampleResponse is the body of the /samples endpoints, which show how
// routes are guarded by an auth.Policy.
type SampleResponse struct {
	Message string `json:"message" example:"Hello, alice"`
	// Policy describes who may call the endpoint.
	Policy string `json:"policy" example:"signed in"`
	// User is the signed-in caller; null for anonymous calls.
	User *UserResponse `json:"user"`
}

// SamplePublic can be called by anyone.
//
//	@Summary		Public sample
//	@Description	Can be called by anyone. Credentials are optional, but rejected when invalid.
//	@Tags			samples
//	@Produce		json
//	@Success		200	{object}	SampleResponse				"Greeting"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Invalid credentials"
//	@Router			/samples/public [get]
func SamplePublic(w http.ResponseWriter, r *http.Request) {
	writeSample(w, r, "public")
}

// SampleSignedIn requires a signed-in user of any role.
//
//	@Summary		Signed-in sample
//	@Description	Requires a signed-in user of any role.
//	@Tags			samples
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	SampleResponse				"Greeting"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Router			/samples/signed-in [get]
func SampleSignedIn(w http.ResponseWriter, r *http.Request) {
	writeSample(w, r, "signed in")
}

// SampleFormCreator requires the FORM_CREATOR or ADMIN role.
//
//	@Summary		Form creator sample
//	@Description	Requires the FORM_CREATOR role (ADMIN passes every role check).
//	@Tags			samples
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	SampleResponse				"Greeting"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Role not allowed"
//	@Router			/samples/form-creator [get]
func SampleFormCreator(w http.ResponseWriter, r *http.Request) {
	writeSample(w, r, "role FORM_CREATOR")
}

// SampleAdmin requires the ADMIN role.
//
//	@Summary		Admin sample
//	@Description	Requires the ADMIN role.
//	@Tags			samples
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	SampleResponse				"Greeting"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Role not allowed"
//	@Router			/samples/admin [get]
func SampleAdmin(w http.ResponseWriter, r *http.Request) {
	writeSample(w, r, "role ADMIN")
}

func writeSample(w http.ResponseWriter, r *http.Request, policy string) {
	resp := SampleResponse{Message: "Hello, anonymous", Policy: policy}
	if u := userFrom(r.Context()); u != nil {
		ur := toUserResponse(*u)
		resp.Message, resp.User = "Hello, "+u.Username, &ur
	}
	writeJSON(w, r, http.StatusOK, resp)
}
