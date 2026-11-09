(function () {
  if (window.__sphInstalled) return;
  window.__sphInstalled = true;

  var API = "http://127.0.0.1:__SPH_PORT__/__sph/download";
  var current = null;
  var busy = false;

  function pickTitle(feed) {
    var d = feed && feed.objectDesc;
    if (d) {
      if (d.description) return d.description;
      if (d.flowCardDesc && d.flowCardDesc.description) return d.flowCardDesc.description;
      if (d.finderNewlifeDesc && d.finderNewlifeDesc.richTextTitle) return d.finderNewlifeDesc.richTextTitle;
    }
    return (feed && (feed.description || feed.id)) || "";
  }

  function onFeed(feed) {
    if (!feed) return;
    var media = feed.objectDesc && feed.objectDesc.media && feed.objectDesc.media[0];
    if (!media || Number(media.mediaType) !== 4) {
      current = null;
      return;
    }
    var spec = "";
    if (media.spec && media.spec.length && media.spec[0] && media.spec[0].fileFormat) {
      spec = String(media.spec[0].fileFormat);
    }
    current = {
      url: (media.url || "") + (media.urlToken || ""),
      key: media.decodeKey == null ? "" : String(media.decodeKey),
      title: pickTitle(feed),
      author: (feed.contact && feed.contact.nickname) || "",
      spec: spec,
    };
    refresh();
  }
  window.__sphOnFeed = onFeed;

  var btn = document.createElement("button");
  btn.type = "button";
  btn.textContent = "下载视频";
  btn.style.cssText = [
    "position:fixed", "right:24px", "bottom:24px", "z-index:2147483647",
    "padding:10px 18px", "border:0", "border-radius:999px",
    "background:#07c160", "color:#fff", "font:600 14px/1.2 system-ui,sans-serif",
    "cursor:pointer", "box-shadow:0 4px 16px rgba(0,0,0,.28)",
    "opacity:.9", "transition:opacity .15s",
  ].join(";");
  btn.onmouseenter = function () { btn.style.opacity = "1"; };
  btn.onmouseleave = function () { btn.style.opacity = ".9"; };

  var tip = document.createElement("div");
  tip.style.cssText = [
    "position:fixed", "right:24px", "bottom:72px", "z-index:2147483647",
    "max-width:320px", "padding:8px 12px", "border-radius:8px",
    "background:rgba(0,0,0,.78)", "color:#fff",
    "font:12px/1.5 system-ui,sans-serif", "display:none", "white-space:pre-wrap",
    "word-break:break-all", "pointer-events:none",
  ].join(";");

  function refresh() {
    btn.textContent = busy ? "下载中…" : "下载视频";
    btn.style.background = current ? "#07c160" : "#8a8a8a";
  }
  function showTip(text, ms) {
    tip.textContent = text;
    tip.style.display = "block";
    if (ms) setTimeout(function () { tip.style.display = "none"; }, ms);
  }

  btn.onclick = async function () {
    if (busy) return;
    if (!current) {
      showTip("还没有捕获到视频，请先打开一个视频号视频。", 3000);
      return;
    }
    busy = true;
    refresh();
    showTip("已提交下载…");
    try {
      var resp = await fetch(API, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(current),
      });
      var data = await resp.json();
      if (data.ok) {
        showTip("已保存：\n" + data.path, 5000);
      } else {
        showTip("下载失败：" + (data.error || "未知错误"), 6000);
      }
    } catch (e) {
      showTip("下载失败：" + e.message, 6000);
    } finally {
      busy = false;
      refresh();
    }
  };

  function mount() {
    if (!document.body) {
      setTimeout(mount, 50);
      return;
    }
    if (!btn.isConnected) document.body.appendChild(btn);
    if (!tip.isConnected) document.body.appendChild(tip);
    refresh();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", mount);
  } else {
    mount();
  }
})();
