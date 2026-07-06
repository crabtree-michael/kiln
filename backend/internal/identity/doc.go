// Package identity owns who the user is and what they have configured
// (docs/specs/11-multi-user.md §2–§4, phase 1): GitHub-derived users, cookie
// sessions, per-user encrypted credentials, and the (single, for now) project.
//
// The runtime does NOT consume this module in phase 1 (11 §1, D6): env vars
// still drive the brain/agent. This module is the dashboard's config store.
package identity
