package download

import (
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/xfy520/m3u8_cli/package/log"
	"github.com/xfy520/m3u8_cli/package/request"
	"github.com/xfy520/m3u8_cli/package/tool"
)

// 下载文件字节流
func HttpDownloadFileToBytes(uri string, headers string, timeOut time.Duration) ([]byte, error) {
	if strings.HasPrefix(uri, "file:") {
		u, err := url.Parse(uri)
		if err != nil {
			log.Error(err.Error())
		}
		uri = u.Path
		sysType := runtime.GOOS
		uri = tool.IfString(sysType == "windows", uri[:1], uri)
		infbytes, err := tool.ReadFile(uri)
		if err != nil {
			return nil, err
		}
		return infbytes, nil
	}
	req, err := request.New(uri, http.MethodGet, timeOut, true)
	req.InitHeader()
	req.SetHeaders(headers)
	if err != nil {
		return nil, err
	}
	return req.Send(-1)
}

func GetWebSource(uri string, headers string, timeOut time.Duration) ([]byte, error) {
	req, err := request.New(uri, http.MethodGet, timeOut, true)
	if err != nil {
		return nil, err
	}
	req.InitHeader()
	req.Set("accept-encoding", "gzip, deflate, br")
	req.Set("keep-alive", "false")
	req.SetHeaders(headers)
	if strings.Contains(uri, "pcvideo") && strings.Contains(uri, ".titan.mgtv.com") {
		if !strings.Contains(uri, "/internettv/") {
			req.Set("referer", "https://www.mgtv.com")
		}
		req.Set("cookie", "MQGUID")
	}
	return req.Send(9)
}
