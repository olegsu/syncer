package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/open-integration/oi"
	airtable_types "github.com/open-integration/oi/catalog/services/airtable/types"
	trello_types "github.com/open-integration/oi/catalog/services/trello/types"
	"github.com/open-integration/oi/core/engine"
	"github.com/open-integration/oi/core/event"
	"github.com/open-integration/oi/core/state"
	"github.com/open-integration/oi/core/task"
	"github.com/pkg/errors"
)

const timeFormat = "02/Jan/2006 15:04"

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
	trelloSvcLocation   = getEnvOrDie("TRELLO_SERVICE_LOCATION")
	airtableSvcLocation = getEnvOrDie("AIRTABLE_SERVICE_LOCATION")
)

func main() {
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
			},
			Reactions: []engine.EventReaction{
				{
					Condition: oi.ConditionEngineStarted(),
					Reaction: func(ev event.Event, state state.State) []task.Task {
						return []task.Task{
							oi.NewSerivceTask("get-cards", "trello", "getcards", trelloArgs...),
						}
					},
				},
				{
					Condition: oi.ConditionTaskFinishedWithStatus("get-cards", state.TaskStatusSuccess),
					Reaction: func(ev event.Event, state state.State) []task.Task {
						t := state.Tasks()
						cards := []trello_types.Card{}
						err := json.Unmarshal([]byte(t["get-cards"].Output), &cards)
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
					},
				},
				{
					Condition: oi.ConditionTaskFinishedWithStatus("get-records", state.TaskStatusSuccess),
					Reaction: func(ev event.Event, state state.State) []task.Task {
						t := state.Tasks()
						cards := []trello_types.Card{}
						err := json.Unmarshal([]byte(t["get-cards"].Output), &cards)
						if err != nil {
							return nil
						}
						records := []airtable_types.Record{}
						err = json.Unmarshal([]byte(t["get-records"].Output), &records)
						if err != nil {
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
									"CreatedAt":  created.Format(timeFormat),
									"ClosedAt":   now.Format(timeFormat),
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
					},
				},
				{
					Condition: oi.ConditionTaskFinishedWithStatus("add-records", state.TaskStatusSuccess),
					Reaction: func(ev event.Event, state state.State) []task.Task {
						t := state.Tasks()
						records := airtable_types.AddRecordsReturns{}
						err := json.Unmarshal([]byte(t["add-records"].Output), &records)
						if err != nil {
							fmt.Println(err)
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
					},
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
