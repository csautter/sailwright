//go:build !unix

package main

func sessionInfo() string {
	return "session info not available"
}
