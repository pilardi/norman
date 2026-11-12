// Package main provides backward-compatible aliases for client utilities.
// All shared logic has been moved to github.com/norman/norman/pkg/client.
package main

import "github.com/norman/norman/pkg/client"

func strVal(s *string) string      { return client.StrVal(s) }
func strPtr(s string) *string      { return client.StrPtr(s) }
func parseFloat(s string) *float32 { return client.ParseFloat(s) }
func parseInt(s string) *int       { return client.ParseInt(s) }
func intPtr(i int) *int            { return client.IntPtr(i) }
