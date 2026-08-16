//go:build !windows

package wintoast

func setAppData(data AppData) error {
	return nil
}

func generateToast(appID string, xml string) error {
	return nil
}

func pushPowershell(xml string) error {
	return nil
}

func pushCOM(xml string) error {
	return nil
}
