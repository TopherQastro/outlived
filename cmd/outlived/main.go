package main

import (
	"context"
	"flag"
	"log"
	"os"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/datastore"
	"github.com/bobg/aesite"
	"github.com/bobg/subcmd"
	"google.golang.org/api/option"
)

func main() {
	var (
		creds      = flag.String("creds", "", "path to credentials file")
		test       = flag.Bool("test", false, "run in test mode")
		projectID  = flag.String("project", "outlived-163105", "Google Cloud project ID")
		locationID = flag.String("location", "us-central1", "location ID")
	)
	flag.Parse()

	if *test && *creds != "" {
		log.Fatal("Cannot supply both -test and -creds")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		done <-chan struct{}
		err  error
	)
	if *test {
		done, err = aesite.DSTestWithDoneChan(ctx, *projectID)
		if err != nil {
			log.Fatalf("Starting test datastore service: %s", err)
		}
	}

	// We are "local" (not talking to real Google Cloud) if we're in test mode
	// or if we've been pointed at a datastore emulator via the standard env var.
	// In that case we must NOT try to create a cloudtasks client, because there
	// is no emulator for cloudtasks and it would demand real credentials.
	usingEmulator := *test || os.Getenv("DATASTORE_EMULATOR_HOST") != ""

	var options []option.ClientOption
	if *creds != "" {
		options = append(options, option.WithCredentialsFile(*creds))
	}

	dsClient, err := datastore.NewClient(ctx, *projectID, options...)
	if err != nil {
		log.Fatalf("Creating datastore client: %s", err)
	}

	var ctClient *cloudtasks.Client
	if !usingEmulator {
		ctClient, err = cloudtasks.NewClient(ctx, options...)
		if err != nil {
			log.Fatalf("Creating cloudtasks client: %s", err)
		}
	}

	c := &maincmd{
		dsClient:   dsClient,
		ctClient:   ctClient,
		locationID: *locationID,
		projectID:  *projectID,
	}

	args := flag.Args()
	if len(args) == 0 {
		args = []string{"serve"}
	}

	err = subcmd.Run(ctx, c, args)
	if err != nil {
		log.Fatal(err)
	}

	if done != nil {
		cancel()
		<-done
	}
}

type maincmd struct {
	dsClient              *datastore.Client
	ctClient              *cloudtasks.Client
	projectID, locationID string
}

func (c *maincmd) Subcmds() subcmd.Map {
	return subcmd.Commands(
		"serve", c.serve, subcmd.Params(
			"content", subcmd.String, "web/public", "path to directory containing static content (for test mode)",
		),
		"admin", c.admin, nil,
	)
}