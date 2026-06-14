package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"cloud.google.com/go/datastore"
	"github.com/bobg/aesite"
	"github.com/bobg/subcmd"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"
	"google.golang.org/api/iterator"

	"outlived"
)

func (c *maincmd) admin(ctx context.Context, args []string) error {
	a := admincmd{c: c}
	return subcmd.Run(ctx, a, args)
}

type admincmd struct {
	c *maincmd
}

func (a admincmd) Subcmds() subcmd.Map {
	return subcmd.Commands(
		"list-figures", a.listFigures, subcmd.Params(
			"died", subcmd.String, "", "died-on date, like Jan-2",
			"limit", subcmd.Int, 100, "limit on figures to return",
		),
		"list-users", a.listUsers, nil,
		"get", a.get, nil,
		"set", a.set, nil,
		"adduser", a.addUser, subcmd.Params(
			"email", subcmd.String, "", "user email",
			"password", subcmd.String, "", "user password",
			"born", subcmd.String, "", "birthdate, like 1987-04-23",
		),
		"scrape", a.scrape, subcmd.Params(
			"month", subcmd.String, "", "3-letter month",
			"day", subcmd.Int, 0, "day of month",
			"limit", subcmd.Duration, time.Second, "rate limit",
		),
	)
}

func (a admincmd) listFigures(ctx context.Context, diedStr string, limit int, _ []string) error {
	died, err := time.Parse("Jan-2", diedStr)
	if err != nil {
		return err
	}

	figs, err := outlived.FiguresDiedOn(ctx, a.c.dsClient, died.Month(), died.Day(), limit)
	if err != nil {
		return err
	}
	for _, fig := range figs {
		fmt.Printf("%s (%s), born %s, died %s, alive %d days, %d pageviews\n", fig.Name, fig.Link, fig.Born, fig.Died, fig.DaysAlive, fig.Pageviews)
	}
	return nil
}

func (a admincmd) listUsers(ctx context.Context, _ []string) error {
	q := datastore.NewQuery("User")
	it := a.c.dsClient.Run(ctx, q)
	for {
		var u outlived.User
		_, err := it.Next(&u)
		if err == iterator.Done {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "iterating over users")
		}

		fmt.Printf("%+v\n", u)
	}
}

func (a admincmd) get(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: outlived admin get VAR")
	}

	val, err := aesite.GetSetting(ctx, a.c.dsClient, args[0])
	if err != nil {
		return err
	}

	fmt.Println(string(val))
	return nil
}

func (a admincmd) set(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: outlived admin set VAR VALUE")
	}

	return aesite.SetSetting(ctx, a.c.dsClient, args[0], []byte(args[1]))
}

// addUser creates a fully-verified, active user directly in the datastore,
// bypassing the e-mail verification flow entirely. Intended for local/test use.
func (a admincmd) addUser(ctx context.Context, email, password, bornStr string, _ []string) error {
	if email == "" || password == "" || bornStr == "" {
		return errors.New("usage: outlived admin adduser -email EMAIL -password PASS -born YYYY-MM-DD")
	}

	born, err := outlived.ParseDate(bornStr)
	if err != nil {
		return errors.Wrap(err, "parsing birthdate")
	}

	u := &outlived.User{
		Born:     born,
		Active:   true,
		TZName:   "America/Los_Angeles",
		TZSector: outlived.TZSector(-8 * 3600), // Pacific, offset in seconds
	}

	// aesite.NewUser hashes the password and stores the user.
	err = aesite.NewUser(ctx, a.c.dsClient, email, password, u)
	if err != nil {
		return errors.Wrap(err, "creating user")
	}

	// Skip e-mail verification: mark verified and write the user back.
	u.Verified = true
	_, err = a.c.dsClient.Put(ctx, u.Key(), u)
	if err != nil {
		return errors.Wrap(err, "marking user verified")
	}

	log.Printf("created verified user %s (born %s)", email, born)
	return nil
}

var daysInMonth = []int{
	0,
	31,
	29,
	31,
	30,
	31,
	30,
	31,
	31,
	30,
	31,
	30,
	31,
}

func (a admincmd) scrape(ctx context.Context, monthStr string, onlyDay int, limit time.Duration, _ []string) error {
	var (
		startMonth = time.January
		endMonth   = time.December
	)
	if monthStr == "" && onlyDay != 0 {
		return errors.New("must specify -month with -day")
	}
	client := &http.Client{
		Transport: &rlroundtripper{
			limiter: rate.NewLimiter(rate.Every(limit), 1),
			rt:      http.DefaultTransport,
		},
	}
	if monthStr != "" {
		d, err := time.Parse("Jan", monthStr)
		if err != nil {
			return err
		}
		if onlyDay != 0 {
			return scrapeMonthDay(ctx, client, a.c.dsClient, d.Month(), onlyDay)
		}
		startMonth = d.Month()
		endMonth = d.Month()
	}
	for m := startMonth; m <= endMonth; m++ {
		for d := 1; d <= daysInMonth[m]; d++ {
			err := scrapeMonthDay(ctx, client, a.c.dsClient, m, d)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func scrapeMonthDay(ctx context.Context, client *http.Client, dsClient *datastore.Client, m time.Month, d int) error {
	return outlived.ScrapeDay(ctx, client, m, d, func(ctx context.Context, href, title, desc string) error {
		log.Printf("scraping %s-%d", m, d)
		err := outlived.ScrapePerson(ctx, client, href, title, desc,
			func(
				ctx context.Context,
				title, desc, href, imgSrc, imgAlt string,
				bornY, bornM, bornD, diedY, diedM, diedD, aliveDays, pageviews int,
			) error {
				log.Printf("updating person %s (href %s), %d pageviews", title, href, pageviews)
				fig := &outlived.Figure{
					Name:      title,
					Desc:      desc,
					Link:      href,
					ImgSrc:    imgSrc,
					ImgAlt:    imgAlt,
					Born:      outlived.Date{Y: bornY, M: time.Month(bornM), D: bornD},
					Died:      outlived.Date{Y: diedY, M: time.Month(diedM), D: diedD},
					DaysAlive: aliveDays,
					Pageviews: pageviews,
					Updated:   time.Now(),
				}
				return outlived.ReplaceFigures(ctx, dsClient, []*outlived.Figure{fig})
			})
		if err != nil {
			log.Printf("ERROR: %s", err)
		}
		return nil
	})
}

type rlroundtripper struct {
	limiter *rate.Limiter
	rt      http.RoundTripper
}

func (rt *rlroundtripper) RoundTrip(req *http.Request) (*http.Response, error) {
	err := rt.limiter.Wait(req.Context())
	if err != nil {
		return nil, err
	}
	return rt.rt.RoundTrip(req)
}