package api

import (
	"context"
	"net/http"
	"testing"
)

func TestDigestScheduleAPIRequiresConfirmationAndUsesCAS(t *testing.T) {
	h := newEmailTestHandler(t)
	if w := emailTestRequest(h, http.MethodGet, "/api/v1/digest/schedule", "", true); w.Code != http.StatusOK {
		t.Fatal("schedule get", w.Code, w.Body.String())
	}
	if w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", `{"enabled":true,"local_time":"09:30","timezone":"Asia/Shanghai","expected_revision":1,"confirmed":false}`, true); w.Code != http.StatusBadRequest {
		t.Fatal("unconfirmed schedule accepted", w.Code, w.Body.String())
	}
	w := emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", `{"enabled":true,"local_time":"09:30","timezone":"Asia/Shanghai","expected_revision":1,"confirmed":true}`, true)
	if w.Code != http.StatusOK {
		t.Fatal("schedule save", w.Code, w.Body.String())
	}
	if w = emailTestRequest(h, http.MethodPatch, "/api/v1/digest/schedule", `{"enabled":false,"local_time":"09:30","timezone":"Asia/Shanghai","expected_revision":1,"confirmed":true}`, true); w.Code != http.StatusConflict {
		t.Fatal("stale schedule accepted", w.Code, w.Body.String())
	}
	if _, err := h.inbox.LoadDigestSchedule(context.Background()); err != nil {
		t.Fatal("schedule storage unavailable", err)
	}
}
