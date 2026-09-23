package googletest

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

/*
The YouTube Reporting API v1 (MH-443):

	GET  /youtubereporting/v1/reportTypes
	GET  /youtubereporting/v1/jobs
	POST /youtubereporting/v1/jobs                      {reportTypeId, name}
	GET  /youtubereporting/v1/jobs/{id}/reports
	GET  /youtubereporting/v1/media/{jobId}/{reportId}   the CSV

Jobs belong to the account (the channel) and are shared by whoever connects
it, as Google's are for one project. A new job has no reports until Google
publishes them (about two days later); PublishReports stands in for that.
Download URLs are on the API's own host, as Google's are.
*/

type fakeJob struct {
	ID, ReportTypeID, Name string
	Created                time.Time
	Reports                []fakeReport
}

type fakeReport struct {
	ID         string
	Day        time.Time
	CreateTime time.Time
}

var fakeReportTypes = []map[string]any{
	{"id": "channel_basic_a1", "name": "Channel basic (old)", "deprecateTime": "2020-01-01T00:00:00Z"},
	{"id": "channel_basic_a2", "name": "Channel basic"},
	{"id": "channel_basic_a3", "name": "Channel basic"},
	{"id": "channel_traffic_source_a2", "name": "Channel traffic sources"},
	{"id": "channel_device_os_a2", "name": "Channel device type and OS"},
	{"id": "channel_combined_a2", "name": "Channel combined"},
	{"id": "content_owner_basic_a3", "name": "Content owner basic", "systemManaged": true},
}

// PublishReports makes Google "publish" one daily report per job of an
// account for each of the last days (ending the day before yesterday).
func (s *Server) PublishReports(sub string, days int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -2)
	for _, j := range s.jobs[sub] {
		have := map[string]bool{}
		for _, r := range j.Reports {
			have[r.Day.Format("2006-01-02")] = true
		}
		for i := days - 1; i >= 0; i-- {
			d := end.AddDate(0, 0, -i)
			if have[d.Format("2006-01-02")] {
				continue
			}
			j.Reports = append(j.Reports, fakeReport{ID: fmt.Sprintf("rep%s%s", j.ID[3:], d.Format("20060102")), Day: d, CreateTime: d.Add(40 * time.Hour)})
		}
	}
}

func (s *Server) ytReporting(w http.ResponseWriter, r *http.Request, p string) bool {
	const root = "/youtubereporting/v1"
	if !strings.HasPrefix(p, root+"/") {
		return false
	}
	rest := strings.TrimPrefix(p, root+"/")
	acct, _, ok := s.begin(w, r, "ytReporting")
	if !ok {
		return true
	}
	switch {
	case rest == "reportTypes" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"reportTypes": fakeReportTypes})
	case rest == "jobs" && r.Method == http.MethodGet:
		s.mu.Lock()
		list := []map[string]any{}
		for _, j := range s.jobs[acct.Sub] {
			list = append(list, map[string]any{"id": j.ID, "reportTypeId": j.ReportTypeID, "name": j.Name, "createTime": j.Created.Format(time.RFC3339)})
		}
		s.mu.Unlock()
		out := map[string]any{}
		if len(list) > 0 {
			out["jobs"] = list
		}
		writeJSON(w, http.StatusOK, out)
	case rest == "jobs" && r.Method == http.MethodPost:
		var req struct {
			ReportTypeID string `json:"reportTypeId"`
			Name         string `json:"name"`
		}
		if err := readJSONBody(r, &req); err != nil || req.ReportTypeID == "" {
			writeAPIError(w, "ytReporting", http.StatusBadRequest, "", "reportTypeId is required.")
			return true
		}
		s.mu.Lock()
		for _, j := range s.jobs[acct.Sub] {
			if j.ReportTypeID == req.ReportTypeID {
				s.mu.Unlock()
				writeAPIError(w, "ytReporting", http.StatusConflict, "", "A job for this report type already exists.")
				return true
			}
		}
		j := &fakeJob{ID: fmt.Sprintf("job%s%02d", pad(acct.Sub, 6), len(s.jobs[acct.Sub])), ReportTypeID: req.ReportTypeID, Name: req.Name, Created: time.Now().UTC()}
		s.jobs[acct.Sub] = append(s.jobs[acct.Sub], j)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"id": j.ID, "reportTypeId": j.ReportTypeID, "name": j.Name, "createTime": j.Created.Format(time.RFC3339)})
	case strings.HasPrefix(rest, "jobs/") && strings.HasSuffix(rest, "/reports") && r.Method == http.MethodGet:
		id := unescapeSegment(strings.TrimSuffix(strings.TrimPrefix(rest, "jobs/"), "/reports"))
		s.mu.Lock()
		var job *fakeJob
		for _, j := range s.jobs[acct.Sub] {
			if j.ID == id {
				job = j
			}
		}
		if job == nil {
			s.mu.Unlock()
			writeAPIError(w, "ytReporting", http.StatusNotFound, "", "Job not found.")
			return true
		}
		reports := append([]fakeReport(nil), job.Reports...)
		origin := s.URL
		s.mu.Unlock()
		sort.Slice(reports, func(i, k int) bool { return reports[i].Day.After(reports[k].Day) })
		list := []map[string]any{}
		for _, rep := range reports {
			list = append(list, map[string]any{"id": rep.ID, "jobId": job.ID,
				"startTime": rep.Day.Format(time.RFC3339), "endTime": rep.Day.AddDate(0, 0, 1).Format(time.RFC3339),
				"createTime": rep.CreateTime.Format(time.RFC3339), "downloadUrl": origin + root + "/media/" + job.ID + "/" + rep.ID})
		}
		out := map[string]any{}
		if len(list) > 0 {
			out["reports"] = list
		}
		writeJSON(w, http.StatusOK, out)
	case strings.HasPrefix(rest, "media/") && r.Method == http.MethodGet:
		parts := strings.Split(strings.TrimPrefix(rest, "media/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return true
		}
		s.mu.Lock()
		var job *fakeJob
		var rep *fakeReport
		for _, j := range s.jobs[acct.Sub] {
			if j.ID == parts[0] {
				job = j
				for i := range j.Reports {
					if j.Reports[i].ID == parts[1] {
						rep = &j.Reports[i]
					}
				}
			}
		}
		d := s.dataOf(acct)
		var vids []YTVideo
		chID := ""
		if d.Channel != nil {
			vids, chID = d.Channel.Videos, d.Channel.ID
		}
		s.mu.Unlock()
		if job == nil || rep == nil {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(reportCSV(job.ReportTypeID, rep.Day, chID, vids)))
	default:
		return false
	}
	return true
}

func reportCSV(typeID string, day time.Time, channelID string, vids []YTVideo) string {
	var b strings.Builder
	date := day.Format("20060102")
	switch {
	case strings.HasPrefix(typeID, "channel_traffic_source"):
		b.WriteString("date,channel_id,video_id,live_or_on_demand,subscribed_status,country_code,traffic_source_type,traffic_source_detail,views,watch_time_minutes,average_view_duration_seconds\n")
		for _, v := range vids {
			for i, src := range []string{"3", "5", "7"} { // search, suggested, browse
				views := 5 + hashN(v.ID+date+src, 60)
				fmt.Fprintf(&b, "%s,%s,%s,on_demand,subscribed,EG,%s,,%d,%d,%d\n", date, channelID, v.ID, src, views, views*2+i, 95+i)
			}
		}
	case strings.HasPrefix(typeID, "channel_device_os"):
		b.WriteString("date,channel_id,video_id,live_or_on_demand,subscribed_status,country_code,device_type,operating_system,views,watch_time_minutes\n")
		for _, v := range vids {
			for _, dev := range []string{"101,24", "102,5"} { // desktop/windows, mobile/android
				views := 4 + hashN(v.ID+date+dev, 50)
				fmt.Fprintf(&b, "%s,%s,%s,on_demand,unsubscribed,EG,%s,%d,%d\n", date, channelID, v.ID, dev, views, views*2)
			}
		}
	default:
		b.WriteString("date,channel_id,video_id,live_or_on_demand,subscribed_status,country_code,views,comments,likes,dislikes,shares,watch_time_minutes,average_view_duration_seconds,subscribers_gained,subscribers_lost\n")
		for _, v := range vids {
			views := 10 + hashN(v.ID+date, 90)
			fmt.Fprintf(&b, "%s,%s,%s,on_demand,subscribed,EG,%d,%d,%d,0,%d,%d,%d,%d,%d\n", date, channelID, v.ID, views,
				hashN(v.ID+date+"c", 3), views/20, hashN(v.ID+date+"s", 4), views*2, 120+hashN(v.ID, 60), hashN(v.ID+date+"g", 3), hashN(v.ID+date+"l", 2))
		}
	}
	return b.String()
}
