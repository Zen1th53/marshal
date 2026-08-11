package model

type Project struct {
	ID            string `json:"id"`
	Repository    string `json:"repository"`
	DefaultBranch string `json:"default_branch"`
	PackVersion   string `json:"pack_version"`
}
