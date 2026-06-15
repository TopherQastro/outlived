package site

import (
	"bytes"
	"context"
	"fmt"
	htemplate "html/template"
	"log"
	"net/http"
	"net/url"
	ttemplate "text/template"

	"github.com/bobg/aesite"
	"github.com/bobg/mid"
	"github.com/pkg/errors"

	"outlived"
)

func (s *Server) handleLogin(
	ctx context.Context,
	req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Forgot   bool   `json:"forgot"`
		TZName   string `json:"tzname"`
	},
) (*userData, error) {
	var (
		now   = tzNow(req.TZName)
		today = outlived.TimeDate(now)
	)

	var u outlived.User

	err := aesite.LookupUser(ctx, s.dsClient, req.Email, &u)
	if err != nil {
		// TODO: distinguish "not found" errors from others
		return nil, errors.Wrapf(err, "looking up user %s", req.Email)
	}

	if req.Forgot {
		expSecs, nonce, vtoken, err := aesite.VerificationToken(&u)
		if err != nil {
			return nil, errors.Wrap(err, "generating verification token")
		}

		link, err := url.Parse(fmt.Sprintf("/s/forgot?e=%d&n=%s&t=%s&u=%s", expSecs, nonce, vtoken, u.Key().Encode()))
		if err != nil {
			return nil, errors.Wrap(err, "constructing forgot-password link")
		}
		link = homeURL.ResolveReference(link)

		dict := map[string]interface{}{"link": link}

		ttmpl, err := ttemplate.New("").Parse(fmailText)
		if err != nil {
			return nil, errors.Wrap(err, "parsing plain-text template")
		}
		textBuf := new(bytes.Buffer)
		err = ttmpl.Execute(textBuf, dict)
		if err != nil {
			return nil, errors.Wrap(err, "executing plain-text template")
		}

		htmpl, err := htemplate.New("").Parse(fmailHTML)
		if err != nil {
			return nil, errors.Wrap(err, "parsing HTML template")
		}
		htmlBuf := new(bytes.Buffer)
		err = htmpl.Execute(htmlBuf, dict)
		if err != nil {
			return nil, errors.Wrap(err, "executing HTML template")
		}

		const subject = "Reset your Outlived password"
		err = s.sender.send(ctx, from, []string{u.Email}, subject, textBuf, htmlBuf)
		if err != nil {
			return nil, errors.Wrap(err, "sending forgot-password mail")
		}

		// xxx
		return nil, nil
	}

	if !u.CheckPW(req.Password) {
		return nil, mid.CodeErr{Err: errors.New("email/password invalid"), C: http.StatusUnauthorized}
	}

	log.Printf("logging in user %s", req.Email)

	sess, err := aesite.NewSession(ctx, s.dsClient, u.Key())
	if err != nil {
		return nil, errors.Wrapf(err, "creating session for user %s", req.Email)
	}
	_, d, err := s.getUserData2(ctx, sess, &u, today, false, 0, 24)
	if err != nil {
		return nil, errors.Wrap(err, "getting user data")
	}

	w := mid.ResponseWriter(ctx)
	sess.SetCookie(w)

	return d, nil
}

const fmailText = `Follow this link to reset your Outlived password:

  {{ .link }}

This link expires in one hour.
`

const fmailHTML = `
<p>Follow <a href="{{ .link }}">this link</a> to reset your Outlived password:</p>
<p><a href="{{ .link }}">{{ .link }}</a></p>
<p>This link expires in one hour.</p>
`

const postForgotTmpl = `
<html>
  <head>
    <title>
      Outlived
    </title>
  </head>
  <body>
    <h1>Check e-mail</h1>

    <p>
      Reset your Outlived password by following the link
      in the e-mail we just sent you. The link expires in one hour.
    </p>

  </body>
</html>
`
