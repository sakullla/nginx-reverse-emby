package pluginsdk

import "testing"

func TestNodeTransferRequestValidateSend(t *testing.T) {
	t.Parallel()
	valid := NodeTransferRequest{
		Action:        NodeTransferActionSend,
		SourceAgentID: "agent-a",
		TargetAgentID: "agent-b",
		AppID:         "app1",
		ArtifactName:  "app1-20261022.tar.zst",
		SizeBytes:     1024,
		SHA256:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid send rejected: %v", err)
	}
	cases := []struct {
		name    string
		mutate  func(*NodeTransferRequest)
		message string
	}{
		{"transfer id set", func(r *NodeTransferRequest) { r.TransferID = "nt-x" }, "transfer_id"},
		{"same agents", func(r *NodeTransferRequest) { r.TargetAgentID = r.SourceAgentID }, "differ"},
		{"missing app", func(r *NodeTransferRequest) { r.AppID = "" }, "app id"},
		{"artifact with slash", func(r *NodeTransferRequest) { r.ArtifactName = "a/b" }, "artifact name"},
		{"artifact traversal", func(r *NodeTransferRequest) { r.ArtifactName = "a..tar" }, "artifact name"},
		{"artifact leading dash", func(r *NodeTransferRequest) { r.ArtifactName = "-x.tar" }, "artifact name"},
		{"zero size", func(r *NodeTransferRequest) { r.SizeBytes = 0 }, "size"},
		{"short sha", func(r *NodeTransferRequest) { r.SHA256 = "abcd" }, "sha256"},
		{"uppercase sha", func(r *NodeTransferRequest) { r.SHA256 = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" }, "sha256"},
		{"unknown action", func(r *NodeTransferRequest) { r.Action = "cancel" }, "unsupported"},
	}
	for _, testCase := range cases {
		request := valid
		testCase.mutate(&request)
		if err := request.Validate(); err == nil {
			t.Fatalf("%s: accepted invalid request", testCase.name)
		}
	}
}

func TestNodeTransferRequestValidateStatus(t *testing.T) {
	t.Parallel()
	if err := (NodeTransferRequest{Action: NodeTransferActionStatus, TransferID: "nt-abc"}).Validate(); err != nil {
		t.Fatalf("valid status rejected: %v", err)
	}
	if err := (NodeTransferRequest{Action: NodeTransferActionStatus}).Validate(); err == nil {
		t.Fatal("status without transfer id accepted")
	}
	withSendFields := NodeTransferRequest{Action: NodeTransferActionStatus, TransferID: "nt-abc", AppID: "app1"}
	if err := withSendFields.Validate(); err == nil {
		t.Fatal("status with send fields accepted")
	}
}

func TestNodeTransferResponseValidate(t *testing.T) {
	t.Parallel()
	if err := (NodeTransferResponse{TransferID: "nt-abc", State: NodeTransferStateCompleted}).Validate(); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	if err := (NodeTransferResponse{TransferID: "nt-abc", State: NodeTransferStateFailed}).Validate(); err == nil {
		t.Fatal("failed state without last error accepted")
	}
	if err := (NodeTransferResponse{TransferID: "nt-abc", State: "weird"}).Validate(); err == nil {
		t.Fatal("unknown state accepted")
	}
	if err := (NodeTransferResponse{TransferID: "nt-abc", State: NodeTransferStateFailed, LastError: "boom\n"}).Validate(); err == nil {
		t.Fatal("unsafe last error accepted")
	}
}
