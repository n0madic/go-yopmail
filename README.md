# go-yopmail

A Go library for interacting with [YOPmail](https://yopmail.com/) - a free disposable email service.

[![Go Reference](https://pkg.go.dev/badge/github.com/n0madic/go-yopmail.svg)](https://pkg.go.dev/github.com/n0madic/go-yopmail)

## Features

- Create disposable email addresses
- Check inbox for received emails
- Read email content
- Delete emails
- Get alternative domain options
- Proxy support

## Installation

```bash
go get github.com/n0madic/go-yopmail
```

## Usage

### Creating a new Yopmail client

```go
// Create a client with a specific username
client, err := yopmail.NewYopmail("yourname", "")

// With proxy support
client, err := yopmail.NewYopmail("yourname", "http://proxy:port")
```

### Checking inbox and reading emails

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/n0madic/go-yopmail"
)

func main() {
	// Create a client
	client, err := yopmail.NewYopmail("testuser", "")
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		return
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get mail IDs from inbox (page 1)
	mailIDs, err := client.GetMailIDs(ctx, 1)
	if err != nil {
		fmt.Printf("Error getting mail IDs: %v\n", err)
		return
	}

	fmt.Printf("Found %d emails in inbox\n", len(mailIDs))

	// Read the first email if any
	if len(mailIDs) > 0 {
		mailHTML, err := client.GetMailBody(ctx, mailIDs[0], false)
		if err != nil {
			fmt.Printf("Error getting email content: %v\n", err)
			return
		}

		fmt.Printf("Email content: %s\n", mailHTML.String())
	}
}
```

### Deleting emails

```go
// Delete a specific email by ID
resp, err := client.DeleteMail(ctx, mailID, 1) // 1 is the page number
```

### Getting alternative domains

```go
// Get a list of all available alternative domains
domains, err := client.GetAlternativeDomains(ctx)
if err != nil {
	fmt.Printf("Error getting domains: %v\n", err)
	return
}

fmt.Printf("Available domains: %v\n", domains)
```

## Error Handling

The library provides specific error constants for common issues:

```go
ErrTooManyRequests // 429 status code, need to use a proxy or wait
ErrVersionNotFound // Couldn't find Yopmail version
ErrYPNotFound      // Couldn't find 'yp' parameter
ErrYJNotFound      // Couldn't find 'yj' parameter
```

## Notes

- To avoid CAPTCHA issues, consider:
  - Using proxies
  - Adding a delay between requests
  - Handling 429 status codes appropriately

## License

[MIT License](LICENSE)