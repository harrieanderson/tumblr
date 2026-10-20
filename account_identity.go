package main

import (
	"fmt"
	"net/http"
	"strings"
)

// Resolve only a blog present in this session. Never silently substitute the
// session's primary blog when the account's expected handle is missing.
func selectVerifiedAccountBlog(id, current, expected string, blogs []tumblrBlog) (tumblrBlog, error) {
	expected = normalizeBlogName(expected)
	var names []string
	for _, b := range blogs {
		names = append(names, "@"+b.Name)
		if expected != "" && normalizeBlogName(b.Name) == expected && b.UUID != "" {
			return b, nil
		}
	}
	if expected != "" {
		return tumblrBlog{}, fmt.Errorf("account %s expects @%s, but saved session owns %s; sign in to the correct account before posting", id, expected, strings.Join(names, ", "))
	}
	for _, b := range blogs {
		if b.UUID != "" && (b.UUID == current || strings.EqualFold(b.Name, current)) {
			return b, nil
		}
	}
	return tumblrBlog{}, fmt.Errorf("account %s has no verified blog mapping; configure its handle before posting", id)
}

func verifyAccountBlog(client *http.Client, auth *Auth) (string, error) {
	id := accountIDFromAuth(*auth)
	blogs, err := listAccountBlogsDetailed(client, *auth)
	if err != nil {
		return "", fmt.Errorf("verify account %s: %w", id, err)
	}
	blog, err := selectVerifiedAccountBlog(id, auth.Blog, assignedBlogName(id), blogs)
	if err != nil {
		return "", err
	}
	auth.Blog = blog.UUID
	return blog.Name, nil
}
