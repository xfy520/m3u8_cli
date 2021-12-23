package parser

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/xfy520/m3u8_cli/package/decode"
	"github.com/xfy520/m3u8_cli/package/download"
	"github.com/xfy520/m3u8_cli/package/ffmpeg"
	"github.com/xfy520/m3u8_cli/package/lang"
	"github.com/xfy520/m3u8_cli/package/log"
	"github.com/xfy520/m3u8_cli/package/request"
	"github.com/xfy520/m3u8_cli/package/tags"
	"github.com/xfy520/m3u8_cli/package/tool"
)

type audio struct {
	Name     string
	Language string
	Uri      string
	Channels string
	ToString func() string
}

type subtitle struct {
	Name     string
	Language string
	Uri      string
	ToString func() string
}

var (
	hasAd      = false
	RangeStart = 0
	RangeEnd   = -1
	DelAd      = true
	DurStart   = ""
	DurEnd     = ""
)

type segInfoObj struct {
	expectByte int64
	startByte  int64
	index      int64
	method     string
	key        string
	iv         string
	duration   float64
	segUri     string
}

func newAudio(Name string, Language string, Uri string, Channels string) *audio {
	return &audio{Name: Language, Uri: Uri, Channels: Channels, ToString: func() string {
		return strings.ReplaceAll("["+Name+"] ["+Language+"] ["+tool.IfString(Channels == "", "", Channels+"ch")+"]", "[]", "")
	}}
}

func newSubtitle(Name string, Language string, Uri string) *subtitle {
	return &subtitle{Name: Language, Uri: Uri, ToString: func() string {
		return "[" + Name + "] [" + Language + "]"
	}}
}

type m3u8Parser struct {
	downloadingM3u8KeyTip bool
	lastKeyLine           string
	m3u8CurrentKey        []string
	m3u8SavePath          string
	jsonSavePath          string
	bestBandwidth         int64
	bestUrl               string
	bestUrlAudio          string
	bestUrlSub            string
	audioUrl              string
	subUrl                string
	extLists              []string
	BaseUrl               string
	M3u8Url               string
	DownDir               string
	DownName              string
	Headers               string
	KeyFile               string
	KeyBase64             string
	LiveStream            bool
	KeyIV                 string
	media_audio_group     map[string][]audio
	media_sub_group       map[string][]subtitle
}

func NewM3u8Parser() *m3u8Parser {
	return &m3u8Parser{
		downloadingM3u8KeyTip: false,
		media_audio_group:     map[string][]audio{},
		media_sub_group:       map[string][]subtitle{},
		lastKeyLine:           "",
		m3u8CurrentKey:        []string{"NONE", "", ""},
		m3u8SavePath:          "",
		jsonSavePath:          "",
		bestBandwidth:         0,
		bestUrl:               "",
		bestUrlAudio:          "",
		bestUrlSub:            "",
		audioUrl:              "",
		subUrl:                "",
		extLists:              []string{},
	}
}

func (p *m3u8Parser) M3u8Parse() {
	ffmpeg.REC_TIME = ""
	p.m3u8SavePath = path.Join(p.DownDir, "raw.m3u8")
	p.jsonSavePath = path.Join(p.DownDir, "meta.json")
	if !tool.Exists(p.DownDir) {
		tool.Check(os.MkdirAll(p.DownDir, os.ModePerm))
	}
	// 存放分部的所有信息(#EXT-X-DISCONTINUITY)
	//存放分片的所有信息
	p.extLists = []string{}

	p.media_audio_group = make(map[string][]audio)
	p.media_sub_group = map[string][]subtitle{}

	var (
		parts          []interface{} = nil
		segments       []segInfoObj  = []segInfoObj{}
		segInfo        segInfoObj    = segInfoObj{}
		m3u8Content    string        = ""
		m3u8Method     string        = ""
		extMAP         []string      = []string{"", ""}
		extList        []string      = []string{}
		segIndex       int64         = 0
		startIndex     int64         = 0
		targetDuration int64         = 0
		totalDuration  float64       = 0
		expectSegment  bool          = false
		expectPlaylist bool          = false
		isEndlist      bool          = false
		isAd           bool          = false
		isM3u          bool          = false
	)

	if strings.Contains(p.M3u8Url, ".cntv.") {
		p.M3u8Url = strings.ReplaceAll(p.M3u8Url, "/h5e/", "/")
	}

	if strings.HasPrefix(p.M3u8Url, "http") {
		if strings.Contains(p.M3u8Url, "nfmovies.com/hls") {
			infbytes, err := download.HttpDownloadFileToBytes(p.M3u8Url, p.Headers, 60)
			tool.Check(err)
			m3u8Content = decode.NfmoviesDecryptM3u8(infbytes)
		} else if strings.Contains(p.M3u8Url, "hls.ddyunp.com/ddyun") || strings.Contains(p.M3u8Url, "hls.90mm.me/ddyun") {
			m3u8Url, err := decode.GetVaildM3u8Url(p.M3u8Url)
			tool.Check(err)
			infbytes, err := download.HttpDownloadFileToBytes(m3u8Url, p.Headers, 60)
			tool.Check(err)
			m3u8Content = decode.DdyunDecryptM3u8(infbytes)
		} else {
			infbytes, err := download.GetWebSource(p.M3u8Url, p.Headers, 60)
			tool.Check(err)
			m3u8Content = tool.BytesToStr(infbytes)
		}
	} else if strings.HasPrefix(p.M3u8Url, "file:") {
		u, err := url.Parse(p.M3u8Url)
		tool.Check(err)
		uri := u.Path
		sysType := runtime.GOOS
		uri = tool.IfString(sysType == "windows", uri[:1], uri)
		infbytes, err := tool.ReadFile(uri)
		tool.Check(err)
		m3u8Content = tool.BytesToStr(infbytes)
	} else if tool.Exists(p.M3u8Url) {
		infbytes, err := tool.ReadFile(p.M3u8Url)
		tool.Check(err)
		m3u8Content = tool.BytesToStr(infbytes)
		if !strings.Contains(m3u8Content, "\\") {
			_, filename, _, _ := runtime.Caller(1)
			p.M3u8Url = path.Join(path.Dir(filename), p.M3u8Url)
		}
		u, err := url.Parse(p.M3u8Url)
		tool.Check(err)
		p.M3u8Url = u.String()
	}

	if m3u8Content == "" {
		tool.Check(errors.New(lang.Lang.ParseError))
	}

	if strings.Contains(p.M3u8Url, "tlivecloud-playback-cdn.ysp.cctv.cn") && strings.Contains(p.M3u8Url, "endtime") {
		isEndlist = true
	}

	if strings.Contains(p.M3u8Url, "imooc.com/") {
		m3u8Data, err := decode.ImoocDecodeM3u8(m3u8Content)
		tool.Check(err)
		m3u8Content = m3u8Data
	}

	// mpd暂定
	if strings.Contains(m3u8Content, "</MPD>") && strings.Contains(m3u8Content, "<MPD") {
		mpdSavePath := path.Join(p.DownDir, "dash.mpd")
		tool.WriteFile(mpdSavePath, m3u8Content)
		req, err := request.New(p.M3u8Url, http.MethodGet, 5, false)
		tool.Check(err)
		req.SetHeaders(p.Headers)
		m3u8Url, err := req.Get302()
		tool.Check(err)
		p.M3u8Url = m3u8Url
		// 分析mpd文件
		newUrl := MpdParse(p.DownDir, p.M3u8Url, m3u8Content, p.BaseUrl)
		p.M3u8Url = newUrl
	}

	// iq暂定
	if strings.HasPrefix(m3u8Content, `{"payload"`) {
		iqJsonPath := path.Join(p.DownDir, "iq.json")
		tool.WriteFile(iqJsonPath, m3u8Content)
		// 分析json文件
		newUrl, err := IqJsonParser(p.DownDir, m3u8Content)
		tool.Check(err)
		p.M3u8Url = newUrl
		u, err := url.Parse(p.M3u8Url)
		tool.Check(err)
		sysType := runtime.GOOS
		pat := tool.IfString(sysType == "windows", u.Path[:1], u.Path)
		byt, err := tool.ReadFile(pat)
		tool.Check(err)
		m3u8Content = tool.BytesToStr(byt)
	}

	tool.WriteFile(p.m3u8SavePath, m3u8Content)

	// //针对优酷#EXT-X-VERSION:7杜比视界片源修正，暂定
	if strings.Contains(m3u8Content, "#EXT-X-DISCONTINUITY") && strings.Contains(m3u8Content, "#EXT-X-MAP") && strings.Contains(m3u8Content, "ott.cibntv.net") && strings.Contains(m3u8Content, "ccode=") {
		reg := regexp.MustCompile("#EXT-X-DISCONTINUITY\\s+#EXT-X-MAP:URI=\\\"(.*?)\\\",BYTERANGE=\\\"(.*?)\\\"")
		_ = reg.FindAllString(m3u8Content, -1)
	}

	//针对Disney+修正，暂定
	if strings.Contains(m3u8Content, "#EXT-X-DISCONTINUITY") && strings.Contains(m3u8Content, "#EXT-X-MAP") && strings.Contains(p.M3u8Url, "media.dssott.com/") {
		_ = regexp.MustCompile("#EXT-X-MAP:URI=\\\".*?BUMPER/[\\s\\S]+?#EXT-X-DISCONTINUITY")
	}

	if strings.Contains(m3u8Content, "#EXT-X-DISCONTINUITY") && strings.Contains(m3u8Content, "#EXT-X-MAP") && (strings.Contains(p.M3u8Url, ".apple.com/")) {
		// Regex.IsMatch(m3u8Content, "#EXT-X-MAP.*\\.apple\\.com/")
		_ = regexp.MustCompile(`(#EXT-X-KEY:[\\s\\S]*?)(#EXT-X-DISCONTINUITY|#EXT-X-ENDLIST)`)
	}

	// 如果BaseUrl为空则截取字符串充当
	if p.BaseUrl == "" {
		matched, err := regexp.MatchString("#YUMING\\|(.*)", m3u8Content)
		tool.Check(err)
		if matched {
			reg := regexp.MustCompile(`#YUMING\\|(.*)`)
			temp := reg.FindAllString(m3u8Content, -1)
			p.BaseUrl = temp[0]
		} else {
			baseUrl, err := getBaseUrl(p.M3u8Url, p.Headers)
			tool.Check(err)
			p.BaseUrl = baseUrl
		}
	}

	if p.KeyBase64 != "" {
		line := tool.IfString(p.KeyIV == "", `#EXT-X-KEY:METHOD=AES-128,URI="base64:`+p.KeyBase64+`"`,
			`#EXT-X-KEY:METHOD=AES-128,URI="base64:`+p.KeyBase64+`",IV=0x`+strings.ReplaceAll(p.KeyIV, "0x", ""))
		p.m3u8CurrentKey = p.ParseKey(line)
	}

	if p.KeyFile != "" {
		u, _ := url.Parse(p.KeyFile)
		line := tool.IfString(p.KeyIV == "", `#EXT-X-KEY:METHOD=AES-128,URI="`+u.String()+`"`,
			`#EXT-X-KEY:METHOD=AES-128,URI="`+u.String()+`",IV=0x`+strings.ReplaceAll(p.KeyIV, "0x", ""))
		p.m3u8CurrentKey = p.ParseKey(line)
	}

	scanner := bufio.NewScanner(strings.NewReader(m3u8Content))
	var (
		segDuration float64 = 0
		segUrl      string  = ""
		expectByte  int64   = -1 //parm n
		startByte   int64   = 0  //parm o
	)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, tags.EXT_M3U) {
			isM3u = true
		} else if strings.HasPrefix(line, tags.EXT_X_BYTERANGE) { //只下载部分字节
			t := strings.Split(strings.ReplaceAll(line, tags.EXT_X_BYTERANGE+":", ""), "@")
			if len(t) > 0 {
				if len(t) == 1 {
					expectByte, _ = strconv.ParseInt(t[0], 10, 64)
					segInfo.expectByte = expectByte
				}
				if len(t) == 2 {
					expectByte, _ = strconv.ParseInt(t[0], 10, 64)
					startByte, _ = strconv.ParseInt(t[1], 10, 64)
					segInfo.expectByte = expectByte
					segInfo.startByte = startByte
				}
			}
			expectSegment = true
		} else if strings.HasPrefix(line, "#UPLYNK-SEGMENT") { //国家地理去广告
			if strings.Contains(line, ",ad") {
				isAd = true
			} else if strings.Contains(line, ",segment") {
				isAd = false
			}
		} else if isAd { //国家地理去广告
			continue
		} else if strings.HasPrefix(line, tags.EXT_X_TARGETDURATION) { //解析定义的分段长度
			f, _ := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(line, tags.EXT_X_TARGETDURATION+":", "")), 64)
			t, _ := strconv.ParseInt(strconv.FormatFloat(f, 'E', -1, 64), 10, 64)
			targetDuration = t
		} else if strings.HasPrefix(line, tags.EXT_X_MEDIA_SEQUENCE) { // 解析起始编号
			segI, _ := strconv.ParseInt(strings.ReplaceAll(line, tags.EXT_X_MEDIA_SEQUENCE+":", ""), 10, 64)
			segIndex = segI
			startIndex = segIndex
		} else if strings.HasPrefix(line, tags.EXT_X_DISCONTINUITY_SEQUENCE) {
		} else if strings.HasPrefix(line, tags.EXT_X_PROGRAM_DATE_TIME) {
			if ffmpeg.REC_TIME != "" {
				ffmpeg.REC_TIME = strings.TrimSpace(strings.ReplaceAll(line, tags.EXT_X_PROGRAM_DATE_TIME+":", ""))
			}
		} else if strings.HasPrefix(line, tags.EXT_X_DISCONTINUITY) { //解析不连续标记，需要单独合并（timestamp不同）
			if hasAd && len(parts) > 0 { //修复优酷去除广告后的遗留问题
				// segments = parts[len(parts)-1]
				parts = append(parts[:len(parts)-1], parts[len(parts):]...)
				hasAd = false
				continue
			}
			if !hasAd && len(segments) > 1 { //常规情况的#EXT-X-DISCONTINUITY标记，新建part
				parts = append(parts, segments)
				segments = []segInfoObj{}
			}
		} else if strings.HasPrefix(line, tags.EXT_X_CUE_OUT) {
		} else if strings.HasPrefix(line, tags.EXT_X_CUE_OUT_START) {
		} else if strings.HasPrefix(line, tags.EXT_X_CUE_SPAN) {
		} else if strings.HasPrefix(line, tags.EXT_X_VERSION) {
		} else if strings.HasPrefix(line, tags.EXT_X_ALLOW_CACHE) {
		} else if strings.HasPrefix(line, tags.EXT_X_KEY) { //解析KEY
			if p.KeyFile != "" || p.KeyBase64 != "" {
				if p.m3u8CurrentKey[2] == "" && strings.Contains(line, "IV=0x") {
					temp := tool.GetTagAttribute(strings.ReplaceAll(line, tags.EXT_X_KEY+":", ""), "IV")
					p.m3u8CurrentKey[2] = temp
				}
			} else {
				p.m3u8CurrentKey = p.ParseKey(line)
				p.lastKeyLine = line
			}
		} else if strings.HasPrefix(line, tags.EXTINF) { // 解析分片时长(暂时不考虑标题属性)
			tmp := strings.Split(strings.ReplaceAll(line, tags.EXTINF+":", ""), ",")
			segDuration, _ = strconv.ParseFloat(tmp[0], 64)
			segInfo.index = segIndex
			segInfo.method = p.m3u8CurrentKey[0]

			if p.m3u8CurrentKey[0] != "NONE" { //是否有加密，有的话写入KEY和IV
				segInfo.key = p.m3u8CurrentKey[1]
				if p.m3u8CurrentKey[2] == "" {
					// 暂定
					segInfo.iv = "0x" + strconv.FormatInt(segIndex, 16)
				} else {
					segInfo.iv = p.m3u8CurrentKey[2]
				}
			}
			totalDuration += segDuration
			segInfo.duration = segDuration
			expectSegment = true
			segIndex++
		} else if strings.HasPrefix(line, tags.EXT_X_STREAM_INF) { //解析STREAM属性
			expectPlaylist = true
			bandwidth := tool.GetTagAttribute(line, "BANDWIDTH")
			average_bandwidth := tool.GetTagAttribute(line, "AVERAGE-BANDWIDTH")
			codecs := tool.GetTagAttribute(line, "CODECS")
			resolution := tool.GetTagAttribute(line, "RESOLUTION")
			frame_rate := tool.GetTagAttribute(line, "FRAME-RATE")
			hdcp_level := tool.GetTagAttribute(line, "HDCP-LEVEL")
			_audio := tool.GetTagAttribute(line, "AUDIO")
			video := tool.GetTagAttribute(line, "VIDEO")
			subtitles := tool.GetTagAttribute(line, "SUBTITLES")
			closed_captions := tool.GetTagAttribute(line, "CLOSED-CAPTIONS")
			extList = []string{bandwidth, average_bandwidth, codecs, resolution,
				frame_rate, hdcp_level, _audio, video, subtitles, closed_captions}
		} else if strings.HasPrefix(line, tags.EXT_X_I_FRAME_STREAM_INF) {
		} else if strings.HasPrefix(line, tags.EXT_X_MEDIA) {
			groupId := tool.GetTagAttribute(line, "GROUP-ID")
			if tool.GetTagAttribute(line, "TYPE") == "AUDIO" {
				channels := tool.GetTagAttribute(line, "CHANNELS")
				language := tool.GetTagAttribute(line, "LANGUAGE")
				name := tool.GetTagAttribute(line, "NAME")
				uri := p.CombineURL(p.BaseUrl, tool.GetTagAttribute(line, "URI"))
				_audio := newAudio(name, language, uri, channels)
				if p.media_audio_group[groupId] == nil {
					p.media_audio_group[groupId] = []audio{*_audio}
				} else {
					p.media_audio_group[groupId] = append(p.media_audio_group[groupId], *_audio)
				}
			} else if tool.GetTagAttribute(line, "TYPE") == "SUBTITLES" {
				language := tool.GetTagAttribute(line, "LANGUAGE")
				name := tool.GetTagAttribute(line, "NAME")
				uri := p.CombineURL(p.BaseUrl, tool.GetTagAttribute(line, "URI"))
				sub := newSubtitle(name, language, uri)
				if p.media_sub_group[groupId] == nil {
					p.media_sub_group[groupId] = []subtitle{*sub}
				} else {
					p.media_sub_group[groupId] = append(p.media_sub_group[groupId], *sub)
				}
			}
		} else if strings.HasPrefix(line, tags.EXT_X_PLAYLIST_TYPE) {
		} else if strings.HasPrefix(line, tags.EXT_I_FRAMES_ONLY) {
		} else if strings.HasPrefix(line, tags.EXT_IS_INDEPENDENT_SEGMENTS) {
		} else if strings.HasPrefix(line, tags.EXT_X_ENDLIST) { //m3u8主体结束
			if len(segments) > 0 {
				parts = append(parts, segments)
			}
			segments = []segInfoObj{}
			isEndlist = true
		} else if strings.HasPrefix(line, tags.EXT_X_MAP) { //#EXT-X-MAP
			if extMAP[0] == "" {
				extMAP[0] = tool.GetTagAttribute(line, "URI")
				if strings.Contains(line, "BYTERANGE") {
					extMAP[1] = tool.GetTagAttribute(line, "BYTERANGE")
				}
				if !strings.HasPrefix(extMAP[0], "http") {
					extMAP[0] = p.CombineURL(p.BaseUrl, extMAP[0])
				}
			} else {
				if len(segments) > 0 {
					parts = append(parts, segments)
				}
				segments = []segInfoObj{}
				isEndlist = true
				break
			}
		} else if strings.HasPrefix(line, tags.EXT_X_START) {
		} else if strings.HasPrefix(line, "#") { //评论行不解析
			continue
		} else if strings.Contains(line, "\r\n") { //空白行不解析
			continue
		} else if expectSegment { //解析分片的地址
			segUrl = p.CombineURL(p.bestUrl, line)
			if strings.Contains(p.M3u8Url, "?__gda__") {
				reg := regexp.MustCompile(`\\?__gda__.*`)
				s := reg.FindAllString(p.M3u8Url, -1)
				if len(s) > 0 {
					segUrl += s[0]
				}
			}
			if strings.Contains(p.M3u8Url, "//dlsc.hcs.cmvideo.cn") && (strings.HasSuffix(segUrl, ".ts") || strings.HasSuffix(segUrl, ".mp4")) {
				reg := regexp.MustCompile(`\\?.*`)
				s := reg.FindAllString(p.M3u8Url, -1)
				if len(s) > 0 {
					segUrl += s[0]
				}
			}
			segInfo.segUri = segUrl
			segments = append(segments, segInfo)
			segInfo = segInfoObj{}

			//优酷的广告分段则清除此分片
			//需要注意，遇到广告说明程序对上文的#EXT-X-DISCONTINUITY做出的动作是不必要的，
			//其实上下文是同一种编码，需要恢复到原先的part上
			if DelAd && strings.Contains(segUrl, "ccode=") && strings.Contains(segUrl, "/ad/") && strings.Contains(segUrl, "duration=") {
				segments = append(segments[:len(segments)-1], segments[len(segments):]...)
				segIndex--
				hasAd = true
			}
			// 优酷广告(4K分辨率测试)
			if DelAd && strings.Contains(segUrl, "ccode=0902") && strings.Contains(segUrl, "duration=") {
				segments = append(segments[:len(segments)-1], segments[len(segments):]...)
				segIndex--
				hasAd = true
			}
			expectSegment = false
		} else if expectPlaylist {
			listUrl := p.CombineURL(p.BaseUrl, line)
			if strings.Contains(p.M3u8Url, "?__gda__") {
				reg := regexp.MustCompile(`\\?__gda__.*`)
				s := reg.FindAllString(p.M3u8Url, -1)
				if len(s) > 0 {
					listUrl += s[0]
				}
			}
			sb := []string{`{"URL":"` + listUrl + `",`}
			for i := 0; i < 10; i++ {
				if extList[i] != "" {
					switch i {
					case 0:
						sb = append(sb, `"BANDWIDTH":"`+extList[i]+`",`)
					case 1:
						sb = append(sb, `"AVERAGE-BANDWIDTH":"`+extList[i]+`",`)
					case 2:
						sb = append(sb, `"CODECS":"`+extList[i]+`",`)
					case 3:
						sb = append(sb, `"RESOLUTION":"`+extList[i]+`",`)
					case 4:
						sb = append(sb, `"FRAME-RATE":"`+extList[i]+`",`)
					case 5:
						sb = append(sb, `"HDCP-LEVEL":"`+extList[i]+`",`)
					case 6:
						sb = append(sb, `"AUDIO":"`+extList[i]+`",`)
					case 7:
						sb = append(sb, `"VIDEO":"`+extList[i]+`",`)
					case 8:
						sb = append(sb, `"SUBTITLES":"`+extList[i]+`",`)
					case 9:
						sb = append(sb, `"CLOSED-CAPTIONS":"`+extList[i]+`",`)
					}
				}
			}
			sb = append(sb, `}`)
			p.extLists = append(p.extLists, strings.ReplaceAll(strings.Join(sb, ""), `,}`, `}`))
			extL, _ := strconv.ParseInt(extList[0], 10, 64)
			if extL >= p.bestBandwidth {
				p.bestBandwidth, _ = strconv.ParseInt(extList[0], 10, 64)
				p.bestUrl = listUrl
				p.bestUrlAudio = extList[6]
				p.bestUrlSub = extList[8]
			}
			extList = []string{}
			expectPlaylist = false
		}
	}

	if !isM3u {
		log.WriteError(lang.Lang.InvalidM3u8Error)
		log.Error(lang.Lang.InvalidM3u8Error)
		return
	}

	if parts == nil {
		parts = append(parts, segments)
	}

  if p.audioUrl != "" && VI
}

func (p *m3u8Parser) ParseKey(line string) []string {
	// if !p.downloadingM3u8KeyTip {
	// 	log.Warn(lang.Lang.DownloadingM3u8Key)
	// 	p.downloadingM3u8KeyTip = true
	// }
	// tmp := strings.Split(strings.ReplaceAll(line, tags.EXT_X_KEY+":", ""), ",")
	// key := []string{"NONE", "", ""}
	// u_l := tool.GetTagAttribute(strings.ReplaceAll(p.lastKeyLine, tags.EXT_X_KEY+":", ""), "URI")
	// m := tool.GetTagAttribute(strings.ReplaceAll(line, tags.EXT_X_KEY+":", ""), "METHOD")
	// u := tool.GetTagAttribute(strings.ReplaceAll(line, tags.EXT_X_KEY+":", ""), "URI")
	// i := tool.GetTagAttribute(strings.ReplaceAll(line, tags.EXT_X_KEY+":", ""), "IV")

	// // 存在加密
	// if m != "" {
	// 	if m != "AES-128" {
	// 		log.Error(fmt.Sprintf(lang.Lang.NotSupportMethodError, m))
	// 		DownloadManager.BinaryMerge = true
	// 		return []string{fmt.Sprintf("%s(NOTSUPPORTED)", m), "", ""}
	// 	}
	// 	key[0] = m
	// 	key[1] = u
	// 	if u_l == u {
	// 		key[1] = p.m3u8CurrentKey[1]
	// 	} else {
	// 		log.WriteInfo(lang.Lang.DownloadingM3u8Key + " " + key[1])
	// 		if strings.HasPrefix(key[1], "http") {
	// 			keyUrl := key[1]
	// 			if strings.Contains(key[1], "imooc.com/") {
	// 				byts, _ := download.GetWebSource(key[1], p.Headers, 60)
	// 				key[1] = decode.ImoocDecodeKey(tool.BytesToStr(byts))
	// 			} else if key[1] == "https://hls.ventunotech.com/m3u8/pc_videosecurevtnkey.key" {
	// 				byts, _ := download.GetWebSource(keyUrl, p.Headers, 60)
	// 				temp := tool.BytesToStr(byts)
	// 				log.Info(temp)
	// 				tempKey := make([]byte, 16)
	// 				for i := 0; i < 16; i++ {
	// 					str := strings.NewReader(temp[i*2 : 2])
	// 					bf := bufio.NewReaderSize(str, 16)
	// 					byt, _ := bf.ReadByte()
	// 					tempKey[i] = byt
	// 				}
	// 				key[1] = base64.StdEncoding.EncodeToString(tempKey)
	// 			} else if strings.Contains(key[1], "drm.vod2.myqcloud.com/getlicense") {
	// 				temp, _ := download.HttpDownloadFileToBytes(keyUrl, p.Headers, 60)
	// 				key[1] = tool.BytesToStr(temp)
	// 			}
	// 		} else {

	// 		}
	// 	}
	// }
	return []string{}
}

func (p *m3u8Parser) CombineURL(baseurl string, uri string) string {
	u, _ := url.Parse(baseurl)
	fmt.Println("===========", baseurl, u.Host)
	uu := u.Scheme + "://" + u.Host
	if strings.HasPrefix(uri, "/") {
		return uu + uri
	} else {
		pa := u.Path
		if strings.HasSuffix(pa, "/") {
			return uu + pa + uri
		}
		lastIndex := strings.LastIndex(pa, "/")
		if lastIndex != -1 {
			pa = pa[:lastIndex]
			return uu + pa + "/" + uri
		}
		return uu + pa + "/" + uri
	}
}

// 获取baseUrl
func getBaseUrl(m3u8url string, headers string) (string, error) {
	req, err := request.New(m3u8url, http.MethodGet, 5, false)
	if err != nil {
		return "", err
	}
	req.SetHeaders(headers)
	m3u8url, err = req.Get302()
	if err != nil {
		return "", err
	}
	if strings.Contains(m3u8url, "?") {
		lastIndex := strings.LastIndex(m3u8url, "?")
		if lastIndex != -1 {
			m3u8url = m3u8url[:lastIndex]
		}
	}
	lastIndex := strings.LastIndex(m3u8url, "/")
	if lastIndex != -1 {
		m3u8url = m3u8url[:lastIndex+1]
	}
	return m3u8url, err
}
