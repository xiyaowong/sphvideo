package inject

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed inject.js
var injectJS string

var (
	reCommentDetail = regexp.MustCompile(`(?s)async finderGetCommentDetail\((\w+)\)\{(.*?)\}async`)
	rePcFlow        = regexp.MustCompile(`(?s)async finderPcFlow\((\w+)\)\{(.*?)\}async`)
	reFlowTab       = regexp.MustCompile(`flowTab:([A-Za-z_$][\w$]*)`)
	reNextFlow      = regexp.MustCompile(`goToNextFlowFeed:([A-Za-z_$][\w$]*)`)
	rePrevFlow      = regexp.MustCompile(`goToPrevFlowFeed:([A-Za-z_$][\w$]*)`)

	reJSFrom       = regexp.MustCompile(`from\s*"([^"]+)\.js"`)
	reJSDependency = regexp.MustCompile(`"js/([^"]+)\.js"`)
	reJSLazy       = regexp.MustCompile(`import\("([^"]+)\.js"\)`)
	reJSImport     = regexp.MustCompile(`import\s*"([^"]+)\.js"`)

	reHTMLScript = regexp.MustCompile(`(src|href)="([^"]+)\.js"`)
)

func scriptFor(port int) string {
	return strings.ReplaceAll(injectJS, "__SPH_PORT__", fmt.Sprint(port))
}

func HTML(html string, port int, version string) string {
	html = reHTMLScript.ReplaceAllString(html, `$1="$2.js?v=`+version+`"`)
	script := "<script>" + scriptFor(port) + "</script>"
	if i := strings.Index(html, "</head>"); i >= 0 {
		return html[:i] + script + html[i:]
	}
	return script + html
}

// Hooks the feed calls and keeps the current feed on navigation.
func Imports(js, version string) string {
	js = reJSFrom.ReplaceAllString(js, `from"$1.js?v=`+version+`"`)
	js = reJSDependency.ReplaceAllString(js, `"js/$1.js?v=`+version+`"`)
	js = reJSLazy.ReplaceAllString(js, `import("$1.js?v=`+version+`")`)
	return reJSImport.ReplaceAllString(js, `import"$1.js?v=`+version+`"`)
}

func ChannelsJS(js, version string) string {
	js = Imports(js, version)

	js = reCommentDetail.ReplaceAllStringFunc(js, func(m string) string {
		sub := reCommentDetail.FindStringSubmatch(m)
		body := sub[2]
		return "async finderGetCommentDetail(" + sub[1] + "){var __sphR=await (async()=>{" + body + "})();" +
			"try{var __sphF=__sphR&&__sphR.data&&__sphR.data.object;if(__sphF&&window.__sphOnFeed)window.__sphOnFeed(__sphF);}catch(e){}" +
			"return __sphR;}async"
	})

	js = rePcFlow.ReplaceAllStringFunc(js, func(m string) string {
		sub := rePcFlow.FindStringSubmatch(m)
		body := sub[2]
		return "async finderPcFlow(" + sub[1] + "){var __sphR=await (async()=>{" + body + "})();" +
			"try{var __sphO=__sphR&&__sphR.data&&__sphR.data.object;if(Array.isArray(__sphO)&&__sphO.length&&__sphO[0]&&window.__sphOnFeed)window.__sphOnFeed(__sphO[0]);}catch(e){}" +
			"return __sphR;}async"
	})

	if m := reFlowTab.FindStringSubmatch(js); len(m) == 2 {
		flow := m[1]
		wrap := func(orig string) string {
			return "async function(v){await " + orig + "(v);try{" +
				"if(typeof " + flow + "!=='undefined'&&" + flow + "&&" + flow + ".value&&" + flow + ".value.feeds){" +
				"var f=" + flow + ".value.feeds[" + flow + ".value.currentFeedIndex];" +
				"if(f&&window.__sphOnFeed)window.__sphOnFeed(f);}}catch(e){}}"
		}
		js = reNextFlow.ReplaceAllStringFunc(js, func(m string) string {
			sub := reNextFlow.FindStringSubmatch(m)
			return "goToNextFlowFeed:" + wrap(sub[1])
		})
		js = rePrevFlow.ReplaceAllStringFunc(js, func(m string) string {
			sub := rePrevFlow.FindStringSubmatch(m)
			return "goToPrevFlowFeed:" + wrap(sub[1])
		})
	}
	return js
}
