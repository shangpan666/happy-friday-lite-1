package main

import (
	"net/http"
	"time"
)

func (a *App) scheduleAPI(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	switch {
	case r.Method == http.MethodGet:
		a.listScheduleEvents(w, uid, id)
	case r.Method == http.MethodPost && id == "":
		a.createScheduleEvent(w, r, uid)
	case r.Method == http.MethodPut && id != "":
		a.updateScheduleEvent(w, r, uid, id)
	case r.Method == http.MethodDelete && id != "":
		a.deleteScheduleEvent(w, uid, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) listScheduleEvents(w http.ResponseWriter, uid int64, id string) {
	query := `SELECT id,title,start_at,end_at,start_time,end_time,all_day,description,color,reminder,completed,priority,created_at,updated_at
		FROM schedule_events WHERE user_id=?`
	args := []any{uid}
	if id != "" {
		query += " AND id=?"
		args = append(args, id)
	}
	query += " ORDER BY start_at ASC,start_time ASC"
	rows, err := a.db.Query(query, args...)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var eventID, title, start, end, startTime, endTime, description, color, priority, created, updated string
		var allDay, reminder, completed int
		if rows.Scan(&eventID, &title, &start, &end, &startTime, &endTime, &allDay, &description, &color, &reminder, &completed, &priority, &created, &updated) == nil {
			out = append(out, scheduleJSON(eventID, title, start, end, startTime, endTime, allDay != 0, description, color, reminder != 0, completed != 0, priority, created, updated))
		}
	}
	if id != "" {
		if len(out) == 0 {
			jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		} else {
			jsonOut(w, http.StatusOK, out[0])
		}
		return
	}
	jsonOut(w, http.StatusOK, out)
}

type scheduleInput struct {
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	AllDay      bool   `json:"allDay"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Reminder    bool   `json:"reminder"`
	Completed   bool   `json:"completed"`
	Priority    string `json:"priority"`
}

func (a *App) createScheduleEvent(w http.ResponseWriter, r *http.Request, uid int64) {
	var in scheduleInput
	if !readJSON(r, &in) {
		jsonOut(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if in.Priority == "" {
		in.Priority = "important"
	}
	if in.Color == "" {
		in.Color = "#60a5fa"
	}
	now, id := time.Now().UTC().Format(time.RFC3339), randomToken()
	_, err := a.db.Exec(`INSERT INTO schedule_events(id,user_id,title,start_at,end_at,start_time,end_time,all_day,description,color,reminder,completed,priority,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, uid, in.Title, in.Start, in.End, in.StartTime, in.EndTime, boolInt(in.AllDay), in.Description, in.Color, boolInt(in.Reminder), boolInt(in.Completed), in.Priority, now, now)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, http.StatusCreated, scheduleJSON(id, in.Title, in.Start, in.End, in.StartTime, in.EndTime, in.AllDay, in.Description, in.Color, in.Reminder, in.Completed, in.Priority, now, now))
}

func (a *App) updateScheduleEvent(w http.ResponseWriter, r *http.Request, uid int64, id string) {
	var in scheduleInput
	if !readJSON(r, &in) {
		jsonOut(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	var exists int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM schedule_events WHERE id=? AND user_id=?", id, uid).Scan(&exists); err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if exists == 0 {
		jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if in.Priority == "" {
		in.Priority = "important"
	}
	if in.Color == "" {
		in.Color = "#60a5fa"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.db.Exec(`UPDATE schedule_events SET title=?,start_at=?,end_at=?,start_time=?,end_time=?,all_day=?,description=?,color=?,reminder=?,completed=?,priority=?,updated_at=? WHERE id=? AND user_id=?`, in.Title, in.Start, in.End, in.StartTime, in.EndTime, boolInt(in.AllDay), in.Description, in.Color, boolInt(in.Reminder), boolInt(in.Completed), in.Priority, now, id, uid)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, http.StatusOK, scheduleJSON(id, in.Title, in.Start, in.End, in.StartTime, in.EndTime, in.AllDay, in.Description, in.Color, in.Reminder, in.Completed, in.Priority, "", now))
}

func (a *App) deleteScheduleEvent(w http.ResponseWriter, uid int64, id string) {
	res, err := a.db.Exec("DELETE FROM schedule_events WHERE id=? AND user_id=?", id, uid)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		jsonOut(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func scheduleJSON(id, title, start, end, startTime, endTime string, allDay bool, description, color string, reminder, completed bool, priority, created, updated string) map[string]any {
	return map[string]any{"id": id, "title": title, "start": start, "end": end, "startTime": startTime, "endTime": endTime, "allDay": allDay, "description": description, "color": color, "reminder": reminder, "completed": completed, "priority": priority, "createdAt": created, "updatedAt": updated}
}
