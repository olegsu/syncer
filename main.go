package main

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/open-integration/oi"
	airtable_types "github.com/open-integration/oi/catalog/services/airtable/types"
	gcalendar_types "github.com/open-integration/oi/catalog/services/google-calendar/types"
	trello_types "github.com/open-integration/oi/catalog/services/trello/types"
	"github.com/open-integration/oi/core/engine"
	"github.com/open-integration/oi/core/event"
	"github.com/open-integration/oi/core/state"
	"github.com/open-integration/oi/core/task"
	"github.com/pkg/errors"
)

const timeFormat = "02/Jan/2006 15:04"

const trelloBlueLabel = "5cdab1c291d0c2ddc5905aad"
const trelloLineLabel = "5cdafec7c65fab3704aaf9bc"
const trelloListToday = "5cdab1cecdf59d48f1546523"

var (
	trelloArgs = []task.Argument{
		{
			Key: "Auth",
			Value: trello_types.Auth{
				App:   getEnvOrDie("TRELLO_APP_ID"),
				Token: getEnvOrDie("TRELLO_TOKEN"),
			},
		},
		{
			Key:   "Board",
			Value: getEnvOrDie("TRELLO_BOARD"),
		},
	}
	airtableArgs = []task.Argument{
		{
			Key: "Auth",
			Value: airtable_types.Auth{
				APIKey:     getEnvOrDie("AIRTABLE_API_KEY"),
				DatabaseID: getEnvOrDie("AIRTABLE_DATABASE_ID"),
				TableName:  getEnvOrDie("AIRTABLE_TABLE_NAME"),
			},
		},
	}
	googleCalendarArgs = []task.Argument{
		{
			Key:   "ShowDeleted",
			Value: false,
		},
		{
			Key:   "TimeMin",
			Value: time.Now().Format(time.RFC3339),
		},
		{
			Key:   "SingleEvents",
			Value: true,
		},
	}
	trelloSvcLocation         = getEnvOrDie("TRELLO_SERVICE_LOCATION")
	airtableSvcLocation       = getEnvOrDie("AIRTABLE_SERVICE_LOCATION")
	googleCalendarSvcLocation = getEnvOrDie("GOOGLE_CALENDAR_SERVICE_LOCATION")
)

var caledarOlegKomodor = getEnvOrDie("WORK_EMAIL")
var caledarOlegPersonal = getEnvOrDie("PERSONAL_EMAIL")

func main() {
	location, _ := time.LoadLocation("UTC")
	pipe := engine.Pipeline{
		Metadata: engine.PipelineMetadata{
			Name: "test",
		},
		Spec: engine.PipelineSpec{
			Services: []engine.Service{
				{
					Name: "trello",
					As:   "trello",
					Path: trelloSvcLocation,
				},
				{
					Name: "airtable",
					As:   "airtable",
					Path: airtableSvcLocation,
				},
				{
					Name: "google-calendar",
					As:   "google-calendar",
					Path: googleCalendarSvcLocation,
				},
			},
			Reactions: []engine.EventReaction{
				{
					Condition: oi.ConditionEngineStarted(),
					Reaction:  getTrelloCards(),
				},
				{
					Condition: oi.ConditionEngineStarted(),
					Reaction:  getEventsFromGoogleCalendar(caledarOlegKomodor, location, getEnvOrDie("GOOGLE_SA_BASE64")),
				},
				{
					Condition: oi.ConditionTaskFinishedWithStatus("get-cards", state.TaskStatusSuccess),
					Reaction:  getAirtableRecords(),
				},
				{
					Condition: oi.ConditionTaskFinishedWithStatus("get-records", state.TaskStatusSuccess),
					Reaction:  createAiretableRecords(location),
				},
				{
					Condition: oi.ConditionTaskFinishedWithStatus("add-records", state.TaskStatusSuccess),
					Reaction:  archiveTrelloCards(),
				},
				{
					Condition: oi.ConditionCombined(
						oi.ConditionTaskFinishedWithStatus(fmt.Sprintf("get-events-%s", caledarOlegKomodor), state.TaskStatusSuccess),
						oi.ConditionTaskFinishedWithStatus("get-cards", state.TaskStatusSuccess),
					),
					Reaction: createTrelloCards(fmt.Sprintf("get-events-%s", caledarOlegKomodor), trelloListToday, []string{trelloBlueLabel}),
				},
				{
					Condition: oi.ConditionCombined(
						oi.ConditionTaskFinishedWithStatus(fmt.Sprintf("get-events-%s", caledarOlegPersonal), state.TaskStatusSuccess),
						oi.ConditionTaskFinishedWithStatus("get-cards", state.TaskStatusSuccess),
					),
					Reaction: createTrelloCards(fmt.Sprintf("get-events-%s", caledarOlegPersonal), trelloListToday, []string{trelloLineLabel}),
				},
			},
		},
	}
	e := oi.NewEngine(&oi.EngineOptions{
		Pipeline: pipe,
	})
	oi.HandleEngineError(e.Run())
}

func xor(a []string, b []string) []string {
	res := []string{}
	for _, i := range a {
		found := false
		for _, j := range b {
			if i == j {
				found = true
				break
			}
		}
		if !found {
			res = append(res, i)
		}
	}

	return res
}

// IDToTime is a convenience function. It takes a Trello ID string and
// extracts the encoded create time as time.Time or an error.
func IDToTime(id string) (t time.Time, err error) {
	if id == "" {
		return time.Time{}, nil
	}
	// The first 8 characters in the object ID are a Unix timestamp
	ts, err := strconv.ParseUint(id[:8], 16, 64)
	if err != nil {
		err = errors.Wrapf(err, "ID '%s' failed to convert to timestamp.", id)
	} else {
		t = time.Unix(int64(ts), 0)
	}
	return
}

func getEnvOrDie(name string) string {
	v := os.Getenv(name)
	if v == "" {
		panic(fmt.Errorf("%s is required", name))
	}
	return v
}

func getTrelloCards() func(ev event.Event, state state.State) []task.Task {
	return func(ev event.Event, state state.State) []task.Task {
		return []task.Task{
			oi.NewSerivceTask("get-cards", "trello", "getcards", trelloArgs...),
		}
	}
}

func getEventsFromGoogleCalendar(calendarID string, location *time.Location, googleSaB64 string) func(ev event.Event, state state.State) []task.Task {
	return func(ev event.Event, state state.State) []task.Task {
		b, err := b64.StdEncoding.DecodeString(googleSaB64)
		if err != nil {
			return nil
		}
		sa := gcalendar_types.ServiceAccount{}
		json.Unmarshal([]byte(b), &sa)
		now := time.Now()
		googleCalendarArgs = append(googleCalendarArgs, task.Argument{
			Key:   "TimeMax",
			Value: time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, location),
		})
		googleCalendarArgs = append(googleCalendarArgs, task.Argument{
			Key:   "ServiceAccount",
			Value: sa,
		})
		googleCalendarArgs = append(googleCalendarArgs, task.Argument{
			Key:   "CalendarID",
			Value: calendarID,
		})
		return []task.Task{
			oi.NewSerivceTask(fmt.Sprintf("get-events-%s", calendarID), "google-calendar", "getevents", googleCalendarArgs...),
		}
	}
}

func getAirtableRecords() func(ev event.Event, state state.State) []task.Task {
	return func(ev event.Event, state state.State) []task.Task {
		cards := []trello_types.Card{}
		err := state.GetStepOutputInto("get-cards", &cards)
		if err != nil {
			return nil
		}
		q := []string{}
		for _, c := range cards {
			q = append(q, fmt.Sprintf("ExternalID='%s'", c.ID))
		}
		a := append(airtableArgs, task.Argument{
			Key:   "Formula",
			Value: fmt.Sprintf("OR(%s)", strings.Join(q, ",")),
		})
		return []task.Task{
			oi.NewSerivceTask("get-records", "airtable", "getrecords", a...),
		}
	}
}

func createAiretableRecords(location *time.Location) func(ev event.Event, state state.State) []task.Task {
	return func(ev event.Event, state state.State) []task.Task {
		cards := []trello_types.Card{}
		err := state.GetStepOutputInto("get-cards", &cards)
		if err != nil {
			return nil
		}
		records := []airtable_types.Record{}
		if err := state.GetStepOutputInto("get-records", &records); err != nil {
			return nil
		}
		candidateIDs := []string{}
		for _, c := range cards {
			if c.List.Name == "Done" {
				candidateIDs = append(candidateIDs, c.ID)
			}
		}
		actualIDs := []string{}
		for _, r := range records {
			if _, ok := r.Fields["ExternalID"]; !ok {
				continue
			}
			actualIDs = append(actualIDs, r.Fields["ExternalID"].(string))
		}
		intersections := xor(candidateIDs, actualIDs)
		candidates := []airtable_types.Record{}
		for _, i := range intersections {
			for _, c := range cards {
				if i != c.ID {
					continue
				}
				if c.List.Name != "Done" {
					continue
				}
				tags := []string{}
				for _, t := range c.Labels {
					if t.Name == "" {
						continue
					}
					tags = append(tags, t.Name)
				}

				created, err := IDToTime(c.ID)
				if err != nil {
					created = time.Now()
				}
				now := time.Now()
				fields := map[string]interface{}{
					"Name":       c.Name,
					"Tags":       tags,
					"Summary":    c.Desc,
					"Project":    "",
					"Link":       c.URL,
					"ExternalID": c.ID,
					"CreatedAt":  created.In(location).Add(time.Hour * 3).Format(timeFormat),
					"ClosedAt":   now.In(location).Add(time.Hour * 3).Format(timeFormat),
				}
				candidates = append(candidates, airtable_types.Record{
					Fields: fields,
				})
			}
		}
		args := append(airtableArgs, task.Argument{
			Key:   "Records",
			Value: candidates,
		})
		return []task.Task{
			oi.NewSerivceTask("add-records", "airtable", "addrecords", args...),
		}
	}
}

func archiveTrelloCards() func(ev event.Event, state state.State) []task.Task {
	return func(ev event.Event, state state.State) []task.Task {
		records := airtable_types.AddRecordsReturns{}
		err := state.GetStepOutputInto("add-records", &records)
		if err != nil {
			return nil
		}
		toArchive := []string{}
		for _, r := range records.Records {
			if _, ok := r.Fields["ExternalID"]; !ok {
				continue
			}
			toArchive = append(toArchive, r.Fields["ExternalID"].(string))
		}
		a := append(trelloArgs, task.Argument{
			Key:   "CardIDs",
			Value: toArchive,
		})
		return []task.Task{
			oi.NewSerivceTask("archive-cards", "trello", "archivecards", a...),
		}
	}
}

func createTrelloCards(calendarEventsTask string, list string, labels []string) func(ev event.Event, state state.State) []task.Task {
	return func(ev event.Event, state state.State) []task.Task {
		cards := []trello_types.Card{}
		if err := state.GetStepOutputInto("get-cards", &cards); err != nil {
			return nil
		}
		events := []gcalendar_types.Event{}
		if err := state.GetStepOutputInto(calendarEventsTask, &events); err != nil {
			return nil
		}
		candidates := map[string]gcalendar_types.Event{}
		for _, e := range events {
			accepted := false
			// accepted the meeting
			for _, a := range e.Attendees {
				if a.Self != nil && *a.Self && a.ResponseStatus != nil && *a.ResponseStatus == "accepted" {
					accepted = true
					break
				}
			}
			// created by me
			if e.Creator != nil && e.Creator.Self != nil && *e.Creator.Self {
				accepted = true
			}
			if !accepted {
				continue
			}
			candidates[*e.ID] = e
		}
		for _, c := range cards {
			if c.List.Name != "Today" {
				continue
			}
			for id := range candidates {
				if strings.Contains(c.Desc, id) {
					delete(candidates, id)
				}
			}
		}
		tasks := []task.Task{}
		for _, c := range candidates {
			name := fmt.Sprintf("add-task-%s", *c.ID)
			desc := []string{}
			if c.Start.DateTime == nil {
				continue
			}
			start, err := time.Parse(time.RFC3339, *c.Start.DateTime)
			if err != nil {
				start = time.Now()
			}
			if c.Description != nil {
				desc = append(desc, *c.Description)
			}
			if c.HTMLLink != nil {
				desc = append(desc, fmt.Sprintf("URL: %s", *c.HTMLLink))
			}
			if c.Start != nil {
				desc = append(desc, fmt.Sprintf("Start At: %s", start.String()))
			}
			desc = append(desc, *c.ID)
			args := append(trelloArgs, task.Argument{
				Key:   "Description",
				Value: strings.Join(desc, "\n"),
			})
			args = append(args, task.Argument{
				Key:   "Labels",
				Value: labels,
			})
			args = append(args, task.Argument{
				Key:   "List",
				Value: list,
			})
			args = append(args, task.Argument{
				Key:   "Name",
				Value: fmt.Sprintf("%s [%s]", *c.Summary, start.Format("15:04")),
			})
			tasks = append(tasks, oi.NewSerivceTask(name, "trello", "addcard", args...))
		}
		return tasks
	}
}
