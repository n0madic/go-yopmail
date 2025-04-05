package yopmail

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	testUsername = "test" // Test mailbox that always contains emails
	testTimeout  = 30 * time.Second
	requestDelay = 100 * time.Millisecond // Delay between requests to avoid CAPTCHA
)

// TestYopmailWorkflow tests the entire Yopmail workflow in a single sequence
func TestYopmailWorkflow(t *testing.T) {
	// Create client
	y, err := NewYopmail(testUsername, "")
	if err != nil {
		t.Fatalf("Failed to create Yopmail client: %v", err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Step 1: Verify client initialization
	t.Log("Step 1: Verifying client initialization")
	if y.Username != testUsername {
		t.Errorf("Expected username %s, got %s", testUsername, y.Username)
	}

	// Sleep to avoid rate limiting
	time.Sleep(requestDelay)

	// Step 2: Check required parameters
	t.Log("Step 2: Checking required parameters")

	// Force initialization of parameters
	resp, err := y.GetInbox(ctx, 1)
	if err != nil {
		t.Fatalf("Failed to get inbox: %v", err)
	}
	resp.Body.Close()

	// Check that required parameters are set
	y.mu.RLock()
	if y.yp == "" {
		t.Error("yp parameter is empty")
	} else {
		t.Logf("yp parameter: %s", y.yp)
	}

	if y.yj == "" {
		t.Error("yj parameter is empty")
	} else {
		t.Logf("yj parameter: %s", y.yj)
	}

	if y.version == "" {
		t.Error("version parameter is empty")
	} else {
		t.Logf("version parameter: %s", y.version)
	}
	y.mu.RUnlock()

	time.Sleep(requestDelay)

	// Step 3: Get mail IDs
	t.Log("Step 3: Getting mail IDs")
	mailIDs, err := y.GetMailIDs(ctx, 1)
	if err != nil {
		t.Fatalf("Failed to get mail IDs: %v", err)
	}

	// Check if we have emails - if not, it might be due to CAPTCHA or an empty inbox
	if len(mailIDs) == 0 {
		t.Log("WARNING: No emails found in test inbox. This could be due to CAPTCHA or an empty inbox.")
		t.Log("Skipping remaining test steps that require emails")
		return
	}

	t.Logf("Found %d emails in test inbox", len(mailIDs))

	// Get first mail ID and verify it's not empty
	firstMailID := mailIDs[0]
	if firstMailID == "" {
		t.Error("First mail ID is empty")
	} else {
		t.Logf("First mail ID: %s", firstMailID)
	}

	time.Sleep(requestDelay)

	// Step 4: Get mail body
	t.Log("Step 4: Getting mail body")
	t.Logf("Using mail ID: %s", firstMailID)

	// Get content directly using mailID as-is
	mailHTML, err := y.GetMailBody(ctx, firstMailID, false)
	if err != nil {
		t.Fatalf("Failed to get mail body: %v", err)
	}

	if mailHTML.HTML == "" {
		t.Error("Expected non-empty mail content, got empty string")
	} else {
		t.Logf("Successfully retrieved email content (length: %d chars)", len(mailHTML.HTML))
		// Print first 100 chars of content for verification
		previewText := strings.TrimSpace(mailHTML.HTML)
		if len(previewText) > 100 {
			previewText = previewText[:100] + "..."
		}
		t.Logf("Content preview: %s", previewText)
	}

	if mailHTML.Username != testUsername {
		t.Errorf("Expected username %s, got %s", testUsername, mailHTML.Username)
	}

	// Skip delete test if there's only one email
	if len(mailIDs) < 2 {
		t.Log("Less than 2 emails in inbox, skipping delete test to preserve content")
		return
	}

	time.Sleep(requestDelay)

	// Step 5: Delete the oldest mail
	t.Log("Step 5: Deleting oldest mail")
	// Take the last email (oldest) for deletion
	oldestMailID := mailIDs[len(mailIDs)-1]
	t.Logf("Attempting to delete mail ID: %s", oldestMailID)

	// Delete the oldest email
	deleteResp, err := y.DeleteMail(ctx, oldestMailID, 1)
	if err != nil {
		t.Fatalf("Failed to delete mail: %v", err)
	}
	deleteResp.Body.Close()

	if deleteResp.StatusCode != 200 {
		t.Errorf("Expected status code 200 for delete operation, got %d", deleteResp.StatusCode)
	}

	time.Sleep(requestDelay)

	// Verify email was deleted
	newMailIDs, err := y.GetMailIDs(ctx, 1)
	if err != nil {
		t.Fatalf("Failed to get mail IDs after deletion: %v", err)
	}

	// Check that deleted email ID is no longer in the list
	deleted := true
	for _, id := range newMailIDs {
		if id == oldestMailID {
			deleted = false
			break
		}
	}

	if deleted {
		t.Log("Successfully deleted oldest email")
	} else {
		t.Errorf("Mail with ID %s was not deleted", oldestMailID)
	}
}

// TestGetAlternativeDomains tests retrieval of alternative domains
func TestGetAlternativeDomains(t *testing.T) {
	// Create client
	y, err := NewYopmail(testUsername, "")
	if err != nil {
		t.Fatalf("Failed to create Yopmail client: %v", err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Add delay to avoid CAPTCHA
	time.Sleep(requestDelay)

	// Get alternative domains
	domains, err := y.GetAlternativeDomains(ctx)
	if err != nil {
		t.Fatalf("Failed to get alternative domains: %v", err)
	}

	// Check that we got some domains
	if len(domains) == 0 {
		t.Error("Expected at least one alternative domain, got none")
	}

	// Check that they look like domains
	domainPattern := regexp.MustCompile(`^[a-zA-Z0-9][-a-zA-Z0-9]*(\.[a-zA-Z0-9][-a-zA-Z0-9]*)+$`)
	validDomains := 0
	for i, domain := range domains {
		if domainPattern.MatchString(domain) {
			validDomains++
		} else {
			t.Logf("Warning: Domain at index %d does not match strict domain pattern: %s", i, domain)
		}
	}

	// Check that at least 50% of domains are valid (in case of pattern issues)
	if float64(validDomains)/float64(len(domains)) < 0.5 {
		t.Errorf("Less than 50%% of domains match the domain pattern (got %d/%d)", validDomains, len(domains))
	}

	// Log some domains for verification
	t.Logf("Found %d alternative domains", len(domains))
	if len(domains) > 5 {
		t.Logf("First 5 domains: %s", strings.Join(domains[:5], ", "))
	} else {
		t.Logf("Domains: %s", strings.Join(domains, ", "))
	}
}
