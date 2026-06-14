package site

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/datastore"
	"github.com/bobg/aesite"
	"github.com/bobg/mid"
	"github.com/pkg/errors"
	"google.golang.org/appengine"

	"outlived"
)

func NewServer(ctx context.Context, contentDir, projectID, locationID string, dsClient *datastore.Client, ctClient *cloudtasks.Client) (*Server, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	s := &Server{
		addr:       addr,
		contentDir: contentDir,
		projectID:  projectID,
		locationID: locationID,
		dsClient:   dsClient,
	}

	if appengine.IsAppEngine() {
		s.tasks = (*gCloudTasks)(ctClient)

		domain, err := aesite.GetSetting(ctx, dsClient, "mailgun_domain")
		if err != nil {
			return nil, errors.Wrap(err, "getting setting for mailgun_domain")
		}
		apiKey, err := aesite.GetSetting(ctx, dsClient, "mailgun_api_key")
		if err != nil {
			return nil, errors.Wrap(err, "getting setting for mailgun_api_key")
		}
		s.sender = newMailgunSender(string(domain), string(apiKey))
	} else {
		s.tasks = newLocalTasks(ctx, addr)
		s.sender = new(testSender)

		// Local/dev convenience: make sure a known, verified user exists so
		// you can log in without the e-mail verification flow. Controlled by
		// env vars, with sensible defaults. Safe to leave in for local runs;
		// it only fires when NOT on App Engine.
		if err := seedDevUser(ctx, dsClient); err != nil {
			return nil, errors.Wrap(err, "seeding dev user")
		}
	}

	return s, nil
}

// seedDevUser creates a verified, active user in the local datastore if one
// does not already exist. Credentials and birthdate come from env vars:
//
//	OUTLIVED_DEV_EMAIL    (default "me@test.com")
//	OUTLIVED_DEV_PASSWORD (default "test1234")
//	OUTLIVED_DEV_BORN     (default "1987-04-23", format YYYY-MM-DD)
//
// This runs only in local mode (see caller) and is a no-op once the user
// exists, so it's safe to run on every startup.
func seedDevUser(ctx context.Context, dsClient *datastore.Client) error {
	email := os.Getenv("OUTLIVED_DEV_EMAIL")
	if email == "" {
		email = "me@test.com"
	}
	password := os.Getenv("OUTLIVED_DEV_PASSWORD")
	if password == "" {
		password = "test1234"
	}
	bornStr := os.Getenv("OUTLIVED_DEV_BORN")
	if bornStr == "" {
		bornStr = "1987-04-23"
	}

	// If the user already exists, do nothing.
	var existing outlived.User
	err := aesite.LookupUser(ctx, dsClient, email, &existing)
	if err == nil {
		log.Printf("dev user %s already present", email)
		return nil
	}
	// Any error other than "not found" is a real problem.
	if !errors.Is(err, datastore.ErrNoSuchEntity) {
		return errors.Wrap(err, "looking up dev user")
	}

	born, err := outlived.ParseDate(bornStr)
	if err != nil {
		return errors.Wrap(err, "parsing OUTLIVED_DEV_BORN")
	}

	u := &outlived.User{
		Born:     born,
		Active:   true,
		TZName:   "America/Los_Angeles",
		TZSector: outlived.TZSector(-8 * 3600), // Pacific, offset in seconds
	}

	// aesite.NewUser hashes the password and stores the user (initially unverified).
	if err := aesite.NewUser(ctx, dsClient, email, password, u); err != nil {
		return errors.Wrap(err, "creating dev user")
	}

	// Mark the embedded aesite.User verified, then write the user back.
	u.User.Verified = true
	if _, err := dsClient.Put(ctx, u.Key(), u); err != nil {
		return errors.Wrap(err, "marking dev user verified")
	}

	log.Printf("seeded verified dev user %s (born %s) - password: %s", email, born, password)
	return nil
}

type Server struct {
	addr       string
	contentDir string
	projectID  string
	locationID string
	dsClient   *datastore.Client
	tasks      taskService
	sender     sender
}

func (s *Server) Serve(ctx context.Context) {
	mux := http.NewServeMux()

	// This is for testing. In production, / is routed by app.yaml.
	mux.Handle("/", mid.Err(s.handleStatic))

	mux.Handle("/s/data", s.sessHandler(mid.JSON(s.handleData)))
	mux.Handle("/s/forgot", mid.Err(s.handleForgot))
	mux.Handle("/s/load", mid.Err(s.handleLoad))
	mux.Handle("/s/login", mid.JSON(s.handleLogin))
	mux.Handle("/s/logout", mid.Err(s.handleLogout))
	mux.Handle("/s/resetpw", mid.Err(s.handleResetPW))
	mux.Handle("/s/reverify", s.sessHandler(mid.JSON(s.handleReverify)))
	mux.Handle("/s/setactive", s.sessHandler(mid.JSON(s.handleSetActive)))
	mux.Handle("/s/setbirthdate", s.sessHandler(mid.JSON(s.handleSetBirthdate)))
	mux.Handle("/s/signup", mid.JSON(s.handleSignup))
	mux.Handle("/s/verify", mid.Err(s.handleVerify))

	mux.Handle("/unsubscribe", http.RedirectHandler("/", http.StatusMovedPermanently))

	mux.Handle("/r", mid.Err(s.handleRedirect))

	// cron-initiated
	mux.Handle("/t/scrape", mid.Err(s.handleScrape))
	mux.Handle("/t/expire", mid.Err(s.handleExpire))
	mux.Handle("/t/send", mid.Err(s.handleSend))

	// task-queue-initiated
	mux.Handle("/t/scrapeday", mid.Err(s.handleScrapeday))
	mux.Handle("/t/scrapeperson", mid.Err(s.handleScrapeperson))

	log.Printf("listening for requests on %s", s.addr)

	srv := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	if appengine.IsAppEngine() {
		err := srv.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	} else {
		go srv.ListenAndServe()
		<-ctx.Done()
		srv.Shutdown(ctx)
	}
}

// Function respWriter wraps an http.ResponseWriter,
// delegating calls to it.
// It tracks whether Write or WriteHeader is ever called.
type respWriter struct {
	w           http.ResponseWriter
	writeCalled bool
}

func (w *respWriter) Header() http.Header {
	return w.w.Header()
}

func (w *respWriter) Write(b []byte) (int, error) {
	w.writeCalled = true
	return w.w.Write(b)
}

func (w *respWriter) WriteHeader(code int) {
	w.writeCalled = true
	w.w.WriteHeader(code)
}

// See
// https://cloud.google.com/appengine/docs/standard/go112/scheduling-jobs-with-cron-yaml#validating_cron_requests.
func (s *Server) checkCron(req *http.Request) error {
	if !appengine.IsAppEngine() {
		return nil
	}

	h := strings.TrimSpace(req.Header.Get("X-Appengine-Cron"))
	if h != "true" {
		return mid.CodeErr{C: http.StatusUnauthorized}
	}
	return nil
}

// See
// https://cloud.google.com/tasks/docs/creating-appengine-handlers#reading_request_headers.
func (s *Server) checkTaskQueue(req *http.Request, queue string) error {
	if !appengine.IsAppEngine() {
		return nil
	}

	ctx := req.Context()
	masterKey, err := aesite.GetSetting(ctx, s.dsClient, "master-key")
	if err == nil && strings.TrimSpace(req.Header.Get("X-Outlived-Key")) == string(masterKey) {
		return nil
	}

	h := strings.TrimSpace(req.Header.Get("X-AppEngine-QueueName"))
	if h != queue {
		return mid.CodeErr{C: http.StatusUnauthorized}
	}
	return nil
}

var homeURL *url.URL

func init() {
	if appengine.IsAppEngine() {
		homeURL = &url.URL{
			Scheme: "https",
			Host:   "outlived.net",
			Path:   "/",
		}
	} else {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		homeURL = &url.URL{
			Scheme: "http",
			Host:   "localhost:" + port,
			Path:   "/",
		}
	}
}