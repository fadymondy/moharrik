package googletest

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

/*
The YouTube Data API v3 list calls (MH-441): playlistItems (the uploads
playlist, paged 50 at a time), videos (by id, up to 50), commentThreads (by
channel or by video). Shaped like Google's: counts are strings; likeCount is
absent when likes are hidden and commentCount when comments are off; a
channel with no uploads answers playlistNotFound (404) for its uploads
playlist; a video with comments off answers commentsDisabled (403).
*/

// YTPageSize caps playlistItems per page (Google: 50). Tests lower it.
var YTPageSize = 50

func (s *Server) youtube(w http.ResponseWriter, r *http.Request, p string) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch p {
	case "/youtube/v3/playlistItems":
		s.playlistItems(w, r)
	case "/youtube/v3/videos":
		s.videos(w, r)
	case "/youtube/v3/commentThreads":
		s.commentThreads(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) playlistItems(w http.ResponseWriter, r *http.Request) {
	_, d, ok := s.begin(w, r, "playlistItems")
	if !ok {
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	ch := d.Channel
	var vids []YTVideo
	if ch != nil {
		vids = append(vids, ch.Videos...)
	}
	s.mu.Unlock()
	if ch == nil || q.Get("playlistId") != "UU"+strings.TrimPrefix(ch.ID, "UC") || len(vids) == 0 {
		writeAPIError(w, "playlistItems", http.StatusNotFound, "playlistNotFound",
			"The playlist identified with the request's <code>playlistId</code> parameter cannot be found.")
		return
	}
	size := YTPageSize
	if n, err := strconv.Atoi(q.Get("maxResults")); err == nil && n > 0 && n < size {
		size = n
	}
	start := 0
	if t := q.Get("pageToken"); t != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(t, "PT"))
		if err != nil {
			writeAPIError(w, "playlistItems", http.StatusBadRequest, "invalidPageToken", "The request specifies an invalid page token.")
			return
		}
		start = n
	}
	items := []map[string]any{}
	for i := start; i < len(vids) && i < start+size; i++ {
		v := vids[i]
		items = append(items, map[string]any{"kind": "youtube#playlistItem", "id": "PLI" + v.ID,
			"contentDetails": map[string]any{"videoId": v.ID, "videoPublishedAt": v.PublishedAt.Format(time.RFC3339)}})
	}
	out := map[string]any{"kind": "youtube#playlistItemListResponse", "items": items,
		"pageInfo": map[string]int{"totalResults": len(vids), "resultsPerPage": size}}
	if start+size < len(vids) {
		out["nextPageToken"] = "PT" + strconv.Itoa(start+size)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) videos(w http.ResponseWriter, r *http.Request) {
	_, d, ok := s.begin(w, r, "videos")
	if !ok {
		return
	}
	ids := strings.Split(r.URL.Query().Get("id"), ",")
	if len(ids) > 50 {
		writeAPIError(w, "videos", http.StatusBadRequest, "invalidFilters", "Too many ids.")
		return
	}
	s.mu.Lock()
	byID := map[string]YTVideo{}
	chID := ""
	if d.Channel != nil {
		chID = d.Channel.ID
		for _, v := range d.Channel.Videos {
			byID[v.ID] = v
		}
	}
	origin := s.URL
	s.mu.Unlock()
	items := []map[string]any{}
	for _, id := range ids {
		v, ok := byID[strings.TrimSpace(id)]
		if !ok {
			continue // Google silently leaves out ids it does not know
		}
		stats := map[string]any{"viewCount": strconv.FormatInt(v.Views, 10), "favoriteCount": "0"}
		if v.Likes >= 0 {
			stats["likeCount"] = strconv.FormatInt(v.Likes, 10)
		}
		if !v.CommentsDisabled {
			stats["commentCount"] = strconv.FormatInt(v.Comments, 10)
		}
		sn := map[string]any{"publishedAt": v.PublishedAt.Format(time.RFC3339), "channelId": chID, "title": v.Title,
			"description": v.Description, "thumbnails": thumbs(origin + "/img/" + v.ID), "channelTitle": "", "categoryId": "22",
			"liveBroadcastContent": "none"}
		if len(v.Tags) > 0 {
			sn["tags"] = v.Tags
		}
		items = append(items, map[string]any{"kind": "youtube#video", "id": v.ID, "snippet": sn,
			"status":         map[string]any{"uploadStatus": "processed", "privacyStatus": v.Privacy, "license": "youtube", "embeddable": true},
			"statistics":     stats,
			"contentDetails": map[string]any{"duration": v.Duration, "dimension": "2d", "definition": "hd", "caption": "false"}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "youtube#videoListResponse", "items": items,
		"pageInfo": map[string]int{"totalResults": len(items), "resultsPerPage": len(items)}})
}

func (s *Server) commentThreads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var ch *YTChannel
	if r.Header.Get("X-Goog-Api-Key") != "" {
		if !s.keyedBegin(w, r, "commentThreads") {
			return
		}
		s.mu.Lock()
		if vid := q.Get("videoId"); vid != "" {
			c, v := s.channelOfVideo(vid)
			if v == nil || v.Privacy != "public" {
				s.mu.Unlock()
				// A key reads public data only: a private video "does not exist".
				writeAPIError(w, "commentThreads", http.StatusNotFound, "videoNotFound",
					"The video identified by the <code><a href=\"/youtube/v3/docs/commentThreads/list#videoId\">videoId</a></code> parameter could not be found.")
				return
			}
			ch = c
		} else {
			ch = s.channelByID(q.Get("allThreadsRelatedToChannelId"))
		}
		s.mu.Unlock()
	} else {
		_, d, ok := s.begin(w, r, "commentThreads")
		if !ok {
			return
		}
		s.mu.Lock()
		ch = d.Channel
		s.mu.Unlock()
	}
	s.mu.Lock()
	var list []YTComment
	disabled := map[string]bool{}
	public := map[string]bool{}
	if ch != nil {
		list = append(list, ch.CommentsList...)
		for _, v := range ch.Videos {
			if v.CommentsDisabled {
				disabled[v.ID] = true
			}
			if v.Privacy == "public" {
				public[v.ID] = true
			}
		}
	}
	keyed := r.Header.Get("X-Goog-Api-Key") != ""
	s.mu.Unlock()
	if vid := q.Get("videoId"); vid != "" {
		if disabled[vid] {
			writeAPIError(w, "commentThreads", http.StatusForbidden, "commentsDisabled",
				"The video identified by the <code><a href=\"/youtube/v3/docs/commentThreads/list#videoId\">videoId</a></code> parameter has disabled comments.")
			return
		}
		keep := list[:0]
		for _, c := range list {
			if c.VideoID == vid {
				keep = append(keep, c)
			}
		}
		list = keep
	} else if ch == nil || q.Get("allThreadsRelatedToChannelId") != ch.ID {
		writeAPIError(w, "commentThreads", http.StatusNotFound, "channelNotFound", "The channel could not be found.")
		return
	}
	if keyed {
		// A key sees comments on public videos only.
		keep := list[:0]
		for _, c := range list {
			if public[c.VideoID] {
				keep = append(keep, c)
			}
		}
		list = keep
	}
	if q.Get("order") == "relevance" {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Likes > list[j].Likes })
	} else {
		sort.SliceStable(list, func(i, j int) bool { return list[i].PublishedAt.After(list[j].PublishedAt) })
	}
	if n, err := strconv.Atoi(q.Get("maxResults")); err == nil && n > 0 && len(list) > n {
		list = list[:n]
	}
	items := []map[string]any{}
	for _, c := range list {
		top := map[string]any{"kind": "youtube#comment", "id": c.ID, "snippet": map[string]any{
			"channelId": ch.ID, "videoId": c.VideoID, "textDisplay": c.Text, "textOriginal": c.Text,
			"authorDisplayName": c.Author, "authorProfileImageUrl": "", "authorChannelUrl": "http://www.youtube.com/channel/" + c.AuthorChannelID,
			"authorChannelId": map[string]string{"value": c.AuthorChannelID}, "canRate": true, "viewerRating": "none",
			"likeCount": c.Likes, "publishedAt": c.PublishedAt.Format(time.RFC3339), "updatedAt": c.PublishedAt.Format(time.RFC3339)}}
		it := map[string]any{"kind": "youtube#commentThread", "id": c.ID, "snippet": map[string]any{
			"channelId": ch.ID, "videoId": c.VideoID, "topLevelComment": top, "canReply": true,
			"totalReplyCount": c.Replies, "isPublic": true}}
		if c.OwnerReplied {
			it["replies"] = map[string]any{"comments": []map[string]any{{"kind": "youtube#comment", "id": c.ID + ".r1",
				"snippet": map[string]any{"authorDisplayName": ch.Title, "authorChannelId": map[string]string{"value": ch.ID},
					"textOriginal": "Thank you!", "parentId": c.ID}}}}
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": "youtube#commentThreadListResponse", "items": items,
		"pageInfo": map[string]int{"totalResults": len(items), "resultsPerPage": len(items)}})
}

// AddComment posts a new comment on a channel (as a viewer would).
func (s *Server) AddComment(sub string, c YTComment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dataOf(Account{Sub: sub})
	if d.Channel != nil {
		d.Channel.CommentsList = append(d.Channel.CommentsList, c)
	}
}
