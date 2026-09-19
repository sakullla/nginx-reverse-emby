package plugins

import "testing"

func TestUIFrontendAssetsAreDataOnlyPackageFiles(t *testing.T) {
	t.Parallel()
	if !isUIFrontendAsset("ui/index.html") || !isSafeAssetName("ui/app.js") || !isSafeAssetName("ui/style.css") {
		t.Fatal("ui/ html, js and css must be allowed as package assets")
	}
	if isExecutableName("ui/app.js") {
		t.Fatal("ui/ javascript must not be classified as an executable asset")
	}
	if isSafeAssetName("app.js") || isUIFrontendAsset("../ui/app.js") || isSafeAssetName("ui/plugin.wasm") {
		t.Fatal("javascript outside ui/ and non-frontend ui files must stay rejected")
	}
}
