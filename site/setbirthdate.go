package site

import (
	"context"
	"net/http"

	"github.com/bobg/mid"
	"github.com/pkg/errors"

	"outlived"
)

func (s *Server) handleSetBirthdate(
	ctx context.Context,
	req struct {
		CSRF    string
		NewDate string
		TZName  string
	},
) (*userData, error) {
	sess := getSess(ctx)
	if sess == nil {
		return nil, mid.CodeErr{C: http.StatusUnauthorized}
	}
	err := sess.CSRFCheck(req.CSRF)
	if err != nil {
		return nil, errors.Wrap(err, "checking CSRF token")
	}
	var (
		now   = tzNow(req.TZName)
		today = outlived.TimeDate(now)
	)
	born, err := outlived.ParseDate(req.NewDate)
	if err != nil {
		return nil, errors.Wrap(mid.CodeErr{C: http.StatusBadRequest, Err: err}, "parsing birthdate")
	}
	var u outlived.User
	err = sess.GetUser(ctx, s.dsClient, &u)
	if err != nil {
		return nil, errors.Wrapf(err, "getting user for session %d", sess.ID)
	}
	u.Born = born
	_, err = s.dsClient.Put(ctx, u.Key(), &u)
	if err != nil {
		return nil, errors.Wrap(err, "storing updated birthdate")
	}
	_, d, err := s.getUserData2(ctx, sess, &u, today, false, 0, 24)
	return d, errors.Wrap(err, "getting updated user data")
}
