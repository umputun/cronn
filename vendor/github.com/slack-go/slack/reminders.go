package slack

import (
	"context"
	"net/url"
)

type Reminder struct {
	ID         string `json:"id"`
	Creator    string `json:"creator"`
	User       string `json:"user"`
	Text       string `json:"text"`
	Recurring  bool   `json:"recurring"`
	Time       int    `json:"time"`
	CompleteTS int    `json:"complete_ts"`
}

type reminderResp struct {
	SlackResponse
	Reminder Reminder `json:"reminder"`
}

type remindersResp struct {
	SlackResponse
	Reminders []*Reminder `json:"reminders"`
}

func (api *Client) doReminder(ctx context.Context, path string, values url.Values) (*Reminder, error) {
	response := &reminderResp{}
	if err := api.postMethod(ctx, path, values, response); err != nil {
		return nil, err
	}
	return &response.Reminder, response.Err()
}

func (api *Client) doReminders(ctx context.Context, path string, values url.Values) ([]*Reminder, error) {
	response := &remindersResp{}
	if err := api.postMethod(ctx, path, values, response); err != nil {
		return nil, err
	}

	// create an array of pointers to reminders
	var reminders = make([]*Reminder, 0, len(response.Reminders))
	reminders = append(reminders, response.Reminders...)
	return reminders, response.Err()
}

// ListReminders lists all the reminders created by or for the authenticated user
// For more details, see ListRemindersContext documentation.
func (api *Client) ListReminders() ([]*Reminder, error) {
	return api.ListRemindersContext(context.Background())
}

// ListRemindersContext lists all the reminders created by or for the authenticated user with a custom context.
// Slack API docs: https://api.slack.com/methods/reminders.list
func (api *Client) ListRemindersContext(ctx context.Context) ([]*Reminder, error) {
	values := url.Values{
		"token": {api.token},
	}
	return api.doReminders(ctx, "reminders.list", values)
}

// AddChannelReminder adds a reminder for a channel.
// For more details, see AddChannelReminderContext documentation.
func (api *Client) AddChannelReminder(channelID, text, time string) (*Reminder, error) {
	return api.AddChannelReminderContext(context.Background(), channelID, text, time)
}

// AddChannelReminderContext adds a reminder for a channel with a custom context
// NOTE: the ability to set reminders on a channel is currently undocumented but has been tested to work.
// Slack API docs: https://api.slack.com/methods/reminders.add
func (api *Client) AddChannelReminderContext(ctx context.Context, channelID, text, time string) (*Reminder, error) {
	values := url.Values{
		"token":   {api.token},
		"text":    {text},
		"time":    {time},
		"channel": {channelID},
	}
	return api.doReminder(ctx, "reminders.add", values)
}

// AddUserReminder adds a reminder for a user.
// For more details, see AddUserReminderContext documentation.
func (api *Client) AddUserReminder(userID, text, time string) (*Reminder, error) {
	return api.AddUserReminderContext(context.Background(), userID, text, time)
}

// AddUserReminderContext adds a reminder for a user with a custom context
// Slack API docs: https://api.slack.com/methods/reminders.add
func (api *Client) AddUserReminderContext(ctx context.Context, userID, text, time string) (*Reminder, error) {
	values := url.Values{
		"token": {api.token},
		"text":  {text},
		"time":  {time},
		"user":  {userID},
	}
	return api.doReminder(ctx, "reminders.add", values)
}

// DeleteReminder deletes an existing reminder.
// For more details, see DeleteReminderContext documentation.
func (api *Client) DeleteReminder(id string) error {
	return api.DeleteReminderContext(context.Background(), id)
}

// DeleteReminderContext deletes an existing reminder with a custom context
// Slack API docs: https://api.slack.com/methods/reminders.delete
func (api *Client) DeleteReminderContext(ctx context.Context, id string) error {
	values := url.Values{
		"token":    {api.token},
		"reminder": {id},
	}
	response := &SlackResponse{}
	if err := api.postMethod(ctx, "reminders.delete", values, response); err != nil {
		return err
	}
	return response.Err()
}
