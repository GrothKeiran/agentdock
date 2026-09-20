package media

import "context"

// PublishBrowserScreenshot 只负责把 Go CDP Service 返回的 PNG 发布为现有 Artifact。
// 浏览器包因此无需了解公开 URL、签名或媒体存储实现。
func (s *Service) PublishBrowserScreenshot(ctx context.Context, png []byte, retentionSeconds int) (map[string]any, error) {
	info, err := identifyImage(png)
	if err != nil {
		return nil, toolError("BINARY_FILE", "browser screenshot is not a supported PNG image", "validation")
	}
	return s.publishImageBytes(ctx, png, "browser-screenshot.png", info, retentionSeconds)
}

// PublishComputerScreenshot publishes a native desktop screenshot through the
// same authenticated Artifact store used by browser automation. The computer
// service separately attaches the bytes as MCP image content so remote AI
// clients can inspect the pixels without needing filesystem access.
func (s *Service) PublishComputerScreenshot(ctx context.Context, png []byte, retentionSeconds int) (map[string]any, error) {
	info, err := identifyImage(png)
	if err != nil || info.MIME != "image/png" {
		return nil, toolError("BINARY_FILE", "computer screenshot is not a supported PNG image", "validation")
	}
	return s.publishImageBytes(ctx, png, "computer-screenshot.png", info, retentionSeconds)
}
