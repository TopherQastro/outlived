package site

import (
	"context"
	"log"
	"time"

	"github.com/bobg/aesite"
	"github.com/pkg/errors"

	"outlived"
)

type (
	dataResp struct {
		// Figures is a list of figures that died on this date.
		Figures []figureData `json:"figures"`

		// Today is today's date.
		Today string `json:"today"`

		// User is info about the signed-in user.
		User *userData `json:"user"`
	}

	figureData struct {
		Name string `json:"name"`
		Desc string `json:"desc"`

		Born string `json:"born"`
		Died string `json:"died"`

		DaysAlive      int    `json:"daysAlive"`
		YearsDaysAlive string `json:"yearsDaysAlive"`

		Href   string `json:"href"`
		ImgAlt string `json:"imgAlt"`
		ImgSrc string `json:"imgSrc"`
	}

	userData struct {
		CSRF           string       `json:"csrf"`
		Born           string       `json:"born"`
		BornYYYYMMDD   string       `json:"bornyyyymmdd"`
		DaysAlive      int          `json:"daysAlive"`
		YearsDaysAlive string       `json:"yearsDaysAlive"`
		Email          string       `json:"email"`
		Figures        []figureData `json:"figures"`
		Verified       bool         `json:"verified"`
		Active         bool         `json:"active"`
	}
)

func (s *Server) handleData(
	ctx context.Context,
	req struct {
		TZName       string `json:"tzname"`
		ByPopularity bool   `json:"byPopularity"` // Added to catch the toggle from frontend
	},
) (*dataResp, error) {
	var (
		now   = tzNow(req.TZName)
		today = outlived.TimeDate(now)
		resp  = &dataResp{Today: now.Format("Monday, 2 Jan 2006")}
		sess  = getSess(ctx)
	)

	figures, err := outlived.FiguresDiedOn(ctx, s.dsClient, today.M, today.D, 24)
	if err != nil {
		return nil, errors.Wrapf(err, "getting figures that died on %d %s", today.D, today.M)
	}
	for _, figure := range figures {
		f := s.toFigureData(figure)
		resp.Figures = append(resp.Figures, f)
	}

	if sess != nil {
		// Pass ByPopularity down the chain
		_, d, err := s.getUserData(ctx, sess, today, req.ByPopularity)
		if err != nil {
			return nil, errors.Wrap(err, "getting user data")
		}
		resp.User = d
	}

	return resp, nil
}

func (s *Server) getUserData(ctx context.Context, sess *aesite.Session, today outlived.Date, byPopularity bool) (*outlived.User, *userData, error) {
	var u outlived.User
	err := sess.GetUser(ctx, s.dsClient, &u)
	if err != nil {
		return nil, nil, errors.Wrap(err, "getting user from session")
	}

	return s.getUserData2(ctx, sess, &u, today, byPopularity)
}

func (s *Server) getUserData2(ctx context.Context, sess *aesite.Session, u *outlived.User, today outlived.Date, byPopularity bool) (*outlived.User, *userData, error) {
	csrf, err := sess.CSRFToken()
	if err != nil {
		return nil, nil, errors.Wrap(err, "generating CSRF token")
	}

	alive := today.Since(u.Born)

	d := &userData{
		CSRF:           csrf,
		Born:           u.Born.String(),
		BornYYYYMMDD:   u.Born.YYYYMMDD(),
		DaysAlive:      alive,
		YearsDaysAlive: today.YDSinceStr(u.Born),
		Email:          u.Email,
		Verified:       u.Verified,
		Active:         u.Active,
	}

	// Pass byPopularity to the datastore query
	figures, err := outlived.FiguresAliveForAtMost(ctx, s.dsClient, alive-1, 24, byPopularity)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "getting figures that died %d days ago", alive-1)
	}
	for _, figure := range figures {
		f := s.toFigureData(figure)
		d.Figures = append(d.Figures, f)
	}

	return u, d, nil
}

func (s *Server) toFigureData(figure *outlived.Figure) figureData {
	return figureData{
		Name:           figure.Name,
		Desc:           figure.Desc,
		Born:           figure.Born.String(),
		Died:           figure.Died.String(),
		DaysAlive:      figure.DaysAlive,
		YearsDaysAlive: figure.Died.YDSinceStr(figure.Born),
		Href:           "https://en.wikipedia.org/wiki/" + figure.Link,
		ImgAlt:         figure.ImgAlt,
		ImgSrc:         figure.ImgSrc,
	}
}

func tzNow(tzname string) time.Time {
	loc, err := time.LoadLocation(tzname)
	if err != nil {
		log.Printf("loading timezone %s (falling back to UTC): %s", tzname, err)
		loc = time.UTC
	}
	return time.Now().In(loc)
}