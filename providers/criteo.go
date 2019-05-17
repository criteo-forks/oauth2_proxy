package providers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/oauth2-proxy/oauth2-proxy/v7/pkg/apis/sessions"
	"github.com/oauth2-proxy/oauth2-proxy/v7/pkg/requests"
)

// CriteoProvider represents a Criteo based Identity Provider
type CriteoProvider struct {
	*ProviderData
	// GroupValidator is a function that determines if the passed email is in
	// the configured groups.
	GroupValidator func(*sessions.SessionState) (bool, error)
	IdentityURL    *url.URL
}

type tokenInfo struct {
	Email string `json:"mail"`
	User  string `json:"uid"`
}

type profileResponse struct {
	Cn     string   `json:"cn"`
	UID    string   `json:"uid"`
	Groups []string `json:"member_of"`
}

// NewCriteoProvider initiates a new CriteoProvider
func NewCriteoProvider(p *ProviderData) *CriteoProvider {
	p.ProviderName = "Criteo"
	if p.Scope == "" {
		p.Scope = "cn mail uid dn umsId"
	}
	return &CriteoProvider{ProviderData: p}
}

// Configure defaults the CriteoProvider configuration options
func (p *CriteoProvider) Configure(ssoHost string, identityHost string, groups []string) {
	p.IdentityURL = &url.URL{Scheme: "http",
		Host: identityHost,
		Path: "/tool/ldapUserInfo/",
	}

	if p.LoginURL.String() == "" {
		p.LoginURL = &url.URL{Scheme: "https",
			Host:     ssoHost,
			Path:     "/auth/oauth2/authorize",
			RawQuery: "realm=criteo",
		}
	}
	if p.RedeemURL.String() == "" {
		p.RedeemURL = &url.URL{Scheme: "https",
			Host:     ssoHost,
			Path:     "/auth/oauth2/access_token",
			RawQuery: "realm=criteo",
		}
	}
	if p.ProfileURL.String() == "" {
		p.ProfileURL = &url.URL{Scheme: "https",
			Host:     ssoHost,
			Path:     "/auth/oauth2/tokeninfo",
			RawQuery: "realm=criteo",
		}
	}
	if p.ValidateURL.String() == "" {
		p.ValidateURL = p.ProfileURL
	}
	p.GroupValidator = func(s *sessions.SessionState) (bool, error) {
		return p.userInGroup(groups, s)
	}
}

func getCriteoHeader(accessToken string) http.Header {
	header := make(http.Header)
	header.Set("Accept", "application/json")
	header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	return header
}

func (p *CriteoProvider) GetProfile(ctx context.Context, s *sessions.SessionState) error {
	if s.User != "" && s.Email != "" {
		return nil
	}

	var info tokenInfo
	err := requests.New(p.ProfileURL.String()).
		WithContext(ctx).
		WithHeaders(getCriteoHeader(s.AccessToken)).
		Do().
		UnmarshalInto(&info)
	if err != nil {
		return err
	}

	s.Email = info.Email
	s.User = info.User
	if s.Email == "" {
		return fmt.Errorf("can't find email")
	}
	return nil
}

func (p *CriteoProvider) getExtendedProfile(dn string) (*profileResponse, error) {
	profile := profileResponse{}

	url := *p.IdentityURL
	url.Path += dn
	err := requests.New(url.String()).
		Do().
		UnmarshalInto(&profile)
	if err != nil {
		return nil, err
	}

	return &profile, nil
}

// GetEmailAddress returns the Account email address
func (p *CriteoProvider) GetEmailAddress(ctx context.Context, s *sessions.SessionState) (string, error) {
	err := p.GetProfile(ctx, s)
	return s.Email, err
}

// GetUserName returns the Account username
func (p *CriteoProvider) GetUserName(ctx context.Context, s *sessions.SessionState) (string, error) {
	err := p.GetProfile(ctx, s)
	return s.User, err
}

// ValidateSessionState validates the AccessToken
func (p *CriteoProvider) ValidateSessionState(ctx context.Context, s *sessions.SessionState) bool {
	return validateToken(ctx, p, s.AccessToken, getCriteoHeader(s.AccessToken))
}

// ValidateGroup validates that the provided email exists in the configured Criteo
// group(s).
func (p *CriteoProvider) Authorize(_ context.Context, s *sessions.SessionState) (bool, error) {
	return p.GroupValidator(s)
}

// RefreshSessionIfNeeded checks if the session has expired and uses the
// RefreshToken to fetch a new ID token if required
func (p *CriteoProvider) RefreshSessionIfNeeded(ctx context.Context, s *sessions.SessionState) (bool, error) {
	if s == nil || (s.ExpiresOn != nil && s.ExpiresOn.After(time.Now())) || s.RefreshToken == "" {
		return false, nil
	}

	ok, err := p.Authorize(ctx, s)
	if err != nil {
		return false, fmt.Errorf("error with authorization for %s", s.Email)
	}
	if !ok {
		return false, fmt.Errorf("%s is no longer in the group(s)", s.Email)
	}

	expires := time.Now().Add(time.Second).Truncate(time.Second)
	origExpiration := s.ExpiresOn
	s.ExpiresOn = &expires
	fmt.Printf("refreshed access token %s (expired on %s)\n", s, origExpiration)
	return false, nil
}

func (p *CriteoProvider) userInGroup(groups []string, s *sessions.SessionState) (bool, error) {
	profile, err := p.getExtendedProfile(s.User)
	if err != nil {
		log.Print(err)
		return false, err
	}

	for _, ug := range profile.Groups {
		for _, g := range groups {
			if ug == g {
				return true, nil
			}
		}
	}
	return false, nil
}
