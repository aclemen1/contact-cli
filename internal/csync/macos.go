package csync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Macos is the backend of Apple Contacts through the macos CLI.
type Macos struct {
	Bin     string
	Timeout time.Duration
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type backendError struct{ msg string }

func (e *backendError) Error() string { return e.msg }

func (m Macos) run(args ...string) (json.RawMessage, error) {
	timeout := m.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args = append(args, "--format", "json")
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	name := strings.Join(args[:min(3, len(args))], " ")
	var env envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%s %s: %v: %s", m.Bin, name, runErr, strings.TrimSpace(errb.String()))
		}
		return nil, fmt.Errorf("%s %s: unreadable output: %.200s", m.Bin, name, out.String())
	}
	if !env.OK {
		msg := "failed"
		if env.Error != nil {
			msg = env.Error.Message
		}
		return nil, &backendError{fmt.Sprintf("%s %s: %s", m.Bin, name, msg)}
	}
	return env.Result, nil
}

type labeled struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

func joinLabeled(l []labeled) []string {
	var out []string
	for _, x := range l {
		if x.Label != "" {
			out = append(out, x.Label+":"+x.Value)
		} else {
			out = append(out, x.Value)
		}
	}
	return out
}

// RawCard is a card as macos contacts get returns it.
type RawCard struct {
	ID           string    `json:"id"`
	ContactType  string    `json:"contactType"`
	GivenName    string    `json:"givenName"`
	FamilyName   string    `json:"familyName"`
	Organization string    `json:"organization"`
	DisplayName  string    `json:"displayName"`
	Phones       []labeled `json:"phones"`
	Emails       []labeled `json:"emails"`
	ContainerID  string    `json:"containerId"`
	IsUnified    bool      `json:"isUnified"`
}

func (r RawCard) Card() *Card {
	return &Card{ID: r.ID, Kind: r.ContactType, Given: r.GivenName, Family: r.FamilyName, Org: r.Organization,
		Phones: joinLabeled(r.Phones), Emails: joinLabeled(r.Emails), Container: r.ContainerID}
}

func (m Macos) Changes(client string) (map[string]bool, bool, string, error) {
	res, err := m.run("contacts", "changes", "--unseen", "--client", client, "--limit", "0")
	if err != nil {
		return nil, false, "", err
	}
	var r struct {
		Changes []struct {
			Kind       string `json:"kind"`
			ContactID  string `json:"contactId"`
			GroupID    string `json:"groupId"`
			SubgroupID string `json:"subgroupId"`
		} `json:"changes"`
		NextToken string `json:"nextToken"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, false, "", err
	}
	touched := map[string]bool{}
	full := false
	for _, c := range r.Changes {
		if c.Kind == "reset" {
			full = true
		}
		for _, id := range []string{c.ContactID, c.GroupID, c.SubgroupID} {
			if id != "" {
				touched[id] = true
			}
		}
	}
	return touched, full, r.NextToken, nil
}

func (m Macos) MarkSeen(client, token string) error {
	_, err := m.run("contacts", "mark-seen", "--client", client, "--token", token)
	return err
}

func (m Macos) Container(name string) (string, error) {
	res, err := m.run("contacts", "containers", "list", "--limit", "0")
	if err != nil {
		return "", err
	}
	var r struct {
		Containers []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return "", err
	}
	var names []string
	for _, c := range r.Containers {
		if c.ID == name || strings.EqualFold(c.Name, name) {
			return c.ID, nil
		}
		names = append(names, c.Name)
	}
	return "", fmt.Errorf("no address book %q; known: %s", name, strings.Join(names, ", "))
}

// GetRaw reads one individual card, never the unified one.
func (m Macos) GetRaw(id string) (*RawCard, error) {
	res, err := m.run("contacts", "get", id, "--no-unify")
	if err != nil {
		if be, ok := err.(*backendError); ok && strings.Contains(strings.ToLower(be.msg), "no contact") {
			return nil, nil
		}
		return nil, err
	}
	var r RawCard
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (m Macos) Get(id string) (*Card, error) {
	r, err := m.GetRaw(id)
	if err != nil || r == nil {
		return nil, err
	}
	return r.Card(), nil
}

func cardArgs(c Card, which []string, update bool) []string {
	var args []string
	has := func(w string) bool {
		if !update {
			return true
		}
		for _, x := range which {
			if x == w {
				return true
			}
		}
		return false
	}
	set := func(flag, v string) {
		switch {
		case v != "":
			args = append(args, "--"+flag, v)
		case update:
			args = append(args, "--clear-"+flag)
		}
	}
	if has("name") {
		if c.Kind == "organization" {
			set("organization", c.Org)
		} else {
			set("given-name", c.Given)
			set("family-name", c.Family)
		}
	}
	if has("org") && c.Kind != "organization" {
		set("organization", c.Org)
	}
	if has("phones") {
		if len(c.Phones) == 0 && update {
			args = append(args, "--clear-phones")
		}
		for _, p := range c.Phones {
			args = append(args, "--phone", p)
		}
	}
	if has("emails") {
		if len(c.Emails) == 0 && update {
			args = append(args, "--clear-emails")
		}
		for _, e := range c.Emails {
			args = append(args, "--email", e)
		}
	}
	return args
}

func (m Macos) Create(container string, c Card) (string, error) {
	args := []string{"contacts", "create", "--contact-type", c.Kind, "--fields", "id"}
	if container != "" {
		args = append(args, "--container-id", container)
	}
	res, err := m.run(append(args, cardArgs(c, nil, false)...)...)
	if err != nil {
		return "", err
	}
	return idOf(res)
}

func (m Macos) Update(id string, c Card, which []string) error {
	args := cardArgs(c, which, true)
	if len(args) == 0 {
		return nil
	}
	_, err := m.run(append([]string{"contacts", "update", id, "--fields", "id"}, args...)...)
	return err
}

func (m Macos) Groups() (map[string]*Group, error) {
	res, err := m.run("contacts", "groups", "list", "--limit", "0")
	if err != nil {
		return nil, err
	}
	var r struct {
		Groups []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			SubgroupIDs []string `json:"subgroupIds"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	out := map[string]*Group{}
	for _, g := range r.Groups {
		members, err := m.Members(g.ID)
		if err != nil {
			return nil, err
		}
		out[g.ID] = &Group{ID: g.ID, Name: g.Name, Members: members, Subgroups: g.SubgroupIDs}
	}
	return out, nil
}

// Members reads the ids of a group's member cards.
func (m Macos) Members(group string) ([]string, error) {
	res, err := m.run("contacts", "list", "--group", group, "--no-unify", "--limit", "0", "--fields", "id")
	if err != nil {
		return nil, err
	}
	var r struct {
		Contacts []struct {
			ID string `json:"id"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range r.Contacts {
		out = append(out, c.ID)
	}
	return out, nil
}

func (m Macos) GroupCreate(container, name string) (string, error) {
	args := []string{"contacts", "groups", "create", name, "--fields", "id"}
	if container != "" {
		args = append(args, "--container-id", container)
	}
	res, err := m.run(args...)
	if err != nil {
		return "", err
	}
	return idOf(res)
}

func (m Macos) GroupRename(id, name string) error {
	_, err := m.run("contacts", "groups", "update", id, "--name", name)
	return err
}

func (m Macos) GroupAdd(group, card string) error {
	_, err := m.run("contacts", "groups", "add", group, card)
	return err
}

func (m Macos) GroupRemove(group, card string) error {
	_, err := m.run("contacts", "groups", "remove", group, card)
	return err
}

func (m Macos) GroupAddSub(parent, sub string) error {
	_, err := m.run("contacts", "groups", "add-subgroup", parent, sub)
	return err
}

func (m Macos) GroupRemoveSub(parent, sub string) error {
	_, err := m.run("contacts", "groups", "remove-subgroup", parent, sub)
	return err
}

func idOf(res json.RawMessage) (string, error) {
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res, &r); err != nil || r.ID == "" {
		return "", fmt.Errorf("no id returned: %.200s", string(res))
	}
	return r.ID, nil
}
