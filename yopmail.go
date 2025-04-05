// Package yopmail provides functionality for interacting with Yopmail service
package yopmail

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	// Pre-compiled regular expressions
	usernameRegex = regexp.MustCompile(`^[-a-zA-Z0-9@_.+]{1,}$`)
	versionRegex  = regexp.MustCompile(`/ver/([0-9.]*)/webmail.js`)
	yjRegex       = regexp.MustCompile(`value\+\'&yj=([0-9a-zA-Z]*)&v=\'`)

	// Common errors
	ErrTooManyRequests = errors.New("too many requests (429 status code) error, use a proxy or try again later")
	ErrVersionNotFound = errors.New("couldn't find Yopmail version")
	ErrYPNotFound      = errors.New("couldn't find 'yp' parameter")
	ErrYJNotFound      = errors.New("couldn't find 'yj' parameter")
)

// YopmailHTML represents the HTML content of an email
type YopmailHTML struct {
	HTML     string
	MailID   string
	Username string
}

// NewYopmailHTML creates a new YopmailHTML instance
func NewYopmailHTML(html, username, mailID string) *YopmailHTML {
	if mailID == "" {
		mailID = generateRandomString(6)
	}

	return &YopmailHTML{
		HTML:     html,
		MailID:   mailID,
		Username: username,
	}
}

// String returns the HTML content
func (y *YopmailHTML) String() string {
	return y.HTML
}

// Yopmail is the main struct for interacting with the Yopmail service
type Yopmail struct {
	Username string
	URL      string
	Client   *http.Client
	Proxies  *url.URL

	// Yopmail needed parameters: protected by mu
	yp      string
	yj      string
	ytime   string
	version string

	mu sync.RWMutex
}

// NewYopmail creates a new Yopmail instance
func NewYopmail(username string, proxies string) (*Yopmail, error) {
	// Validate username
	if !usernameRegex.MatchString(username) {
		return nil, errors.New("username is not valid")
	}

	// Split username at '@' and take the first part
	usernameOnly := strings.Split(username, "@")[0]

	// Create cookie jar for handling cookies
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	// Create an optimized HTTP transport
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
	}

	client := &http.Client{
		Jar:       jar,
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	yopmail := &Yopmail{
		Username: usernameOnly,
		URL:      "https://yopmail.com/en/",
		Client:   client,
		version:  "9.0", // Default version
	}

	// Set up proxy if provided
	if proxies != "" {
		proxyURL, err := url.Parse(proxies)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		yopmail.Proxies = proxyURL
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	// Find version
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if version, err := yopmail.FindVersion(ctx); err == nil {
		yopmail.version = version
	}

	return yopmail, nil
}

// FindVersion finds the Yopmail version from the website
func (y *Yopmail) FindVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", y.URL, nil)
	if err != nil {
		return "", err
	}

	resp, err := y.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return "", ErrTooManyRequests
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	// Looking for: <script src="/ver/[VERSION]/webmail.js"></script>
	version := ""
	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			if matches := versionRegex.FindStringSubmatch(src); len(matches) > 1 {
				version = matches[1]
			}
		}
	})

	if version != "" {
		y.mu.Lock()
		y.version = version
		y.mu.Unlock()
		return version, nil
	}

	return "", ErrVersionNotFound
}

// Request makes a request to the Yopmail service with necessary parameters
func (y *Yopmail) Request(ctx context.Context, requestURL string, params url.Values, contextDesc string) (*http.Response, error) {
	// Check and initialize parameters if needed
	if err := y.ensureParameters(ctx); err != nil {
		return nil, fmt.Errorf("[x] Couldn't initialize parameters for %s request: %w", contextDesc, err)
	}

	// Add required parameters
	y.mu.RLock()
	if params == nil {
		params = url.Values{}
	}

	if y.yp != "" && params.Get("yp") == "" {
		params.Set("yp", y.yp)
	}

	if y.yj != "" && params.Get("yj") == "" {
		params.Set("yj", y.yj)
	}

	if y.version != "" && params.Get("v") == "" {
		params.Set("v", y.version)
	}
	y.mu.RUnlock()

	// Add ytime
	ytime := y.addYtime()

	// Add query parameters to URL
	reqURL := requestURL
	if len(params) > 0 {
		reqURL = fmt.Sprintf("%s?%s", requestURL, params.Encode())
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("[x] Couldn't create request for %s: %w", contextDesc, err)
	}

	// Add ytime cookie to request URL domain
	reqURLParsed, err := url.Parse(requestURL)
	if err == nil {
		y.Client.Jar.SetCookies(reqURLParsed, []*http.Cookie{
			{
				Name:  "ytime",
				Value: ytime,
				Path:  "/",
			},
		})
	}

	// Make request
	resp, err := y.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("[x] Couldn't process %s request: %w", contextDesc, err)
	}

	// Check for status code
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		if resp.StatusCode == 429 {
			return nil, ErrTooManyRequests
		}
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp, nil
}

// EnsureParameters ensures that all required parameters (yp, yj) are initialized
func (y *Yopmail) ensureParameters(ctx context.Context) error {
	// Use atomic checks to minimize lock contention
	y.mu.RLock()
	needYP := y.yp == ""
	needYJ := y.yj == ""
	y.mu.RUnlock()

	if needYP {
		if err := y.extractYP(ctx); err != nil {
			return fmt.Errorf("failed to extract yp: %w", err)
		}
	}

	if needYJ {
		if err := y.extractYJ(ctx); err != nil {
			return fmt.Errorf("failed to extract yj: %w", err)
		}
	}

	return nil
}

// ExtractYP extracts the 'yp' parameter from the Yopmail website
func (y *Yopmail) extractYP(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", y.URL, nil)
	if err != nil {
		return err
	}

	resp, err := y.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return ErrTooManyRequests
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}

	// Looking for value of an hidden input element with 'yp' as name and id
	yp := ""
	doc.Find("input[name='yp'][id='yp']").Each(func(i int, s *goquery.Selection) {
		if val, exists := s.Attr("value"); exists {
			yp = val
		}
	})

	if yp == "" {
		return ErrYPNotFound
	}

	y.mu.Lock()
	y.yp = yp
	y.mu.Unlock()

	return nil
}

// ExtractYJ extracts the 'yj' parameter from the Yopmail webmail.js file
func (y *Yopmail) extractYJ(ctx context.Context) error {
	y.mu.RLock()
	version := y.version
	y.mu.RUnlock()

	reqURL := fmt.Sprintf("https://yopmail.com/ver/%s/webmail.js", version)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return err
	}

	resp, err := y.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return ErrTooManyRequests
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	bodyText := string(bodyBytes)
	matches := yjRegex.FindStringSubmatch(bodyText)

	if len(matches) < 2 {
		return ErrYJNotFound
	}

	y.mu.Lock()
	y.yj = matches[1]
	y.mu.Unlock()

	return nil
}

// AddYtime adds the 'ytime' parameter and returns the current value
func (y *Yopmail) addYtime() string {
	now := time.Now()
	ytime := fmt.Sprintf("%d:%d", now.Hour(), now.Minute())

	y.mu.Lock()
	y.ytime = ytime
	y.mu.Unlock()

	return ytime
}

// GetInbox gets the inbox contents
func (y *Yopmail) GetInbox(ctx context.Context, page int) (*http.Response, error) {
	params := url.Values{
		"login": {y.Username},
		"p":     {fmt.Sprintf("%d", page)},
		"d":     {""},
		"ctrl":  {""},
		"r_c":   {""},
		"id":    {""},
		"ad":    {"0"},
	}

	return y.Request(ctx, fmt.Sprintf("%sinbox", y.URL), params, "inbox")
}

// GetMailIDs gets mail IDs from the inbox
func (y *Yopmail) GetMailIDs(ctx context.Context, page int) ([]string, error) {
	resp, err := y.GetInbox(ctx, page)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	mailIDs := make([]string, 0, 10) // Preallocate a reasonable size
	doc.Find("div.m").Each(func(i int, s *goquery.Selection) {
		if id, exists := s.Attr("id"); exists {
			mailIDs = append(mailIDs, id)
		}
	})

	return mailIDs, nil
}

// GetMailBody gets the body of a mail
func (y *Yopmail) GetMailBody(ctx context.Context, mailID string, showImage bool) (*YopmailHTML, error) {
	// Determine ID prefix based on whether to show images
	finalID := "m" + mailID
	if showImage {
		finalID = "i" + mailID
	}

	params := url.Values{
		"b":  {y.Username},
		"id": {finalID},
	}

	resp, err := y.Request(ctx, fmt.Sprintf("%smail", y.URL), params, "mail body")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	mailHTML := ""
	if mailDiv := doc.Find("div#mail"); mailDiv.Length() > 0 {
		if html, err := goquery.OuterHtml(mailDiv); err == nil {
			mailHTML = html
		}
	}

	return NewYopmailHTML(mailHTML, y.Username, finalID), nil
}

// DeleteMail deletes a mail
func (y *Yopmail) DeleteMail(ctx context.Context, mailID string, page int) (*http.Response, error) {
	params := url.Values{
		"login": {y.Username},
		"p":     {fmt.Sprintf("%d", page)},
		"d":     {mailID},
		"ctrl":  {""},
		"r_c":   {""},
		"id":    {""},
		"ad":    {"0"},
	}

	return y.Request(ctx, fmt.Sprintf("%sinbox", y.URL), params, "delete mail")
}

// GetAlternativeDomains returns a list of alternative domains available on Yopmail
func (y *Yopmail) GetAlternativeDomains(ctx context.Context) ([]string, error) {
	// URL for getting the domain list with parameter to show all domains
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://yopmail.com/en/domain?d=all", nil)
	if err != nil {
		return nil, err
	}

	// Use the existing client rather than creating a new one
	resp, err := y.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	// Extract domains with capacity estimation
	domains := make([]string, 0, 20)
	doc.Find("div.lstdom > div").Each(func(i int, s *goquery.Selection) {
		domain := strings.TrimPrefix(s.Text(), "@")
		domains = append(domains, domain)
	})

	return domains, nil
}

// Helper function to generate random string using crypto/rand
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	randomBytes := make([]byte, length)

	_, err := rand.Read(randomBytes)
	if err != nil {
		// Fallback to a less secure method if crypto/rand fails
		for i := range result {
			result[i] = charset[time.Now().UnixNano()%int64(len(charset))]
		}
		return string(result)
	}

	for i, b := range randomBytes {
		result[i] = charset[int(b)%len(charset)]
	}
	return string(result)
}
