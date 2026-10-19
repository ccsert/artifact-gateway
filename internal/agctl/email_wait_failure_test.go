package agctl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEmailDeliveryWaitReportsDeadAsFailureAndRetainsRecord(t *testing.T) {
	api := newRouteAPI(t, "")
	path := "/api/v2/email-deliveries/" + deliveryID
	record := strings.Replace(deliveryJSON("dead", "4"), `"attempts":1,"possibleDuplicate":false`, `"attempts":4,"possibleDuplicate":true,"errorCode":"smtp_permanent_rejection"`, 1)
	api.on("GET", path, 200, record)
	code, stdout, stderr := runCLI(t, "email", "delivery", "wait", deliveryID, "--json")
	if code != ExitFailure {
		t.Fatalf("dead delivery exit=%d stdout=%s stderr=%s, want exit 1", code, stdout, stderr)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "delivery_failed" || envelope.Error.HTTPStatus != nil {
		t.Fatalf("dead delivery must have a local terminal failure: %s", stdout)
	}
	var actual, expected map[string]any
	if err := json.Unmarshal(envelope.Data, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(record), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("terminal failure changed the delivery record: actual=%v expected=%v", actual, expected)
	}
	if calls := api.recorded(); len(calls) != 1 || calls[0].Method != "GET" || calls[0].Path != path {
		t.Fatalf("waiting must not queue, replay or cancel a delivery: %+v", calls)
	}
	assertGolden(t, "email_delivery_wait_dead", stdout)

	code, stdout, stderr = runCLI(t, "email", "delivery", "wait", deliveryID)
	if code != ExitFailure || !strings.Contains(stdout, "dead") || !strings.Contains(stdout, "smtp_permanent_rejection") || !strings.Contains(stderr, "email delivery failed") {
		t.Fatalf("human failure must retain state and reason: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}
