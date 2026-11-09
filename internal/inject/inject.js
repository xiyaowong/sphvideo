(function () {
  if (window.__sphInstalled) return;
  window.__sphInstalled = true;

  var API = "http://127.0.0.1:__SPH_PORT__/__sph/download";
  var current = null;
  var busy = false;
  var busyKind = "";

  function pickTitle(feed) {
    var d = feed && feed.objectDesc;
    if (d) {
      if (d.description) return d.description;
      if (d.flowCardDesc && d.flowCardDesc.description) return d.flowCardDesc.description;
      if (d.finderNewlifeDesc && d.finderNewlifeDesc.richTextTitle) return d.finderNewlifeDesc.richTextTitle;
    }
    return (feed && (feed.description || feed.id)) || "";
  }

  function pickAuthor(feed) {
    return (feed && feed.contact && feed.contact.nickname) || "";
  }

  function mediaURL(media) {
    return ((media && media.url) || "") + ((media && media.urlToken) || "");
  }

  function specLabel(spec) {
    var parts = [];
    var w = Number(spec.width);
    var h = Number(spec.height);
    if (isFinite(w) && isFinite(h) && w > 0 && h > 0) parts.push(w + "×" + h);
    var codec = spec.codingFormat || spec.codec;
    if (codec) parts.push(String(codec));
    if (spec.fileFormat) parts.push(String(spec.fileFormat));
    return parts.join(" ") || "画质";
  }

  function onFeed(feed) {
    if (!feed) {
      current = null;
      hint.textContent = "等待视频号内容…";
      refresh();
      return;
    }
    if (feed.liveInfo) {
      current = null;
      hint.textContent = "暂不支持直播";
      refresh();
      return;
    }
    var d = feed.objectDesc;
    if (!d) {
      current = null;
      hint.textContent = "等待视频号内容…";
      refresh();
      return;
    }
    var list = d.media || [];
    var first = list[0];
    var type = Number(d.mediaType || (first && first.mediaType) || (feed.files && feed.files.length ? 2 : 0));
    if (type === 2) {
      var mediaList = (feed.files && feed.files.length) ? feed.files : list;
      var files = [];
      for (var i = 0; i < mediaList.length; i++) {
        var u = mediaURL(mediaList[i]);
        if (u) files.push(u);
      }
      var coverItem = mediaList[0] || first;
      var music = d.followPostInfo && d.followPostInfo.musicInfo;
      current = {
        type: "images",
        files: files,
        bgm: (music && music.mediaStreamingUrl) || "",
        title: pickTitle(feed),
        author: pickAuthor(feed),
        cover: (coverItem && (coverItem.coverUrl || coverItem.thumbUrl || coverItem.fullThumbUrl || mediaURL(coverItem))) || "",
      };
    } else if (type === 4 && first) {
      var specs = [];
      var seen = {};
      var rawSpecs = first.spec || [];
      for (var j = 0; j < rawSpecs.length; j++) {
        var s = rawSpecs[j];
        if (!s || !s.fileFormat || seen[s.fileFormat]) continue;
        seen[s.fileFormat] = true;
        specs.push({ value: String(s.fileFormat), label: specLabel(s) });
      }
      current = {
        type: "video",
        url: mediaURL(first),
        key: first.decodeKey == null ? "" : String(first.decodeKey),
        title: pickTitle(feed),
        author: pickAuthor(feed),
        cover: first.coverUrl || first.thumbUrl || first.fullThumbUrl || "",
        specs: specs,
      };
    } else {
      current = null;
      hint.textContent = "等待视频号内容…";
    }
    refresh();
  }
  window.__sphOnFeed = onFeed;

  var panel = document.createElement("div");
  panel.style.cssText = [
    "position:fixed", "right:24px", "bottom:24px", "z-index:2147483647",
    "display:flex", "flex-direction:column", "align-items:flex-end", "gap:8px",
    "padding:10px", "border-radius:12px",
    "background:rgba(255,255,255,.96)", "box-shadow:0 6px 24px rgba(0,0,0,.22)",
    "font:13px/1.2 system-ui,-apple-system,sans-serif",
  ].join(";");

  var row = document.createElement("div");
  row.style.cssText = "display:flex;align-items:center;gap:6px;";
  panel.appendChild(row);

  var quality = document.createElement("select");
  quality.style.cssText = [
    "max-width:180px", "padding:6px 8px", "border:1px solid #d0d0d0",
    "border-radius:6px", "background:#fff", "color:#222", "font:12px/1.2 system-ui,sans-serif",
    "outline:none",
  ].join(";");
  row.appendChild(quality);

  var buttons = {};
  ["video", "mp3", "cover", "images"].forEach(function (kind) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = { video: "视频", mp3: "MP3", cover: "封面", images: "图片" }[kind];
    b.style.cssText = [
      "padding:7px 12px", "border:0", "border-radius:999px",
      "background:#07c160", "color:#fff", "font:600 12px/1.2 system-ui,sans-serif",
      "cursor:pointer", "white-space:nowrap",
    ].join(";");
    b.onclick = function () { send(kind); };
    buttons[kind] = b;
    row.appendChild(b);
  });

  var tip = document.createElement("div");
  tip.style.cssText = [
    "max-width:320px", "padding:6px 10px", "border-radius:8px",
    "background:rgba(0,0,0,.78)", "color:#fff",
    "font:12px/1.5 system-ui,sans-serif", "display:none", "white-space:pre-wrap",
    "word-break:break-all", "pointer-events:none",
  ].join(";");
  panel.appendChild(tip);

  var hint = document.createElement("div");
  hint.textContent = "等待视频号内容…";
  hint.style.cssText = "padding:2px 4px;color:#666;font:12px/1.2 system-ui,sans-serif;";
  panel.appendChild(hint);

  function showTip(text, ms) {
    tip.textContent = text;
    tip.style.display = "block";
    if (ms) setTimeout(function () {
      if (tip.textContent === text) tip.style.display = "none";
    }, ms);
  }

  function refresh() {
    var type = current && current.type;
    panel.style.opacity = current ? "1" : ".65";
    hint.style.display = current ? "none" : "";
    quality.style.display = type === "video" ? "" : "none";
    if (type === "video") {
      var prev = quality.value;
      quality.innerHTML = "";
      var opt = document.createElement("option");
      opt.value = "";
      opt.textContent = "原始画质";
      quality.appendChild(opt);
      for (var i = 0; i < current.specs.length; i++) {
        var o = document.createElement("option");
        o.value = current.specs[i].value;
        o.textContent = current.specs[i].label;
        quality.appendChild(o);
      }
      quality.value = prev || "";
      if (quality.selectedIndex < 0) quality.selectedIndex = 0;
    }
    buttons.video.style.display = type === "video" ? "" : "none";
    buttons.mp3.style.display = type === "video" ? "" : "none";
    buttons.cover.style.display = current && current.cover ? "" : "none";
    buttons.images.style.display = type === "images" ? "" : "none";
    Object.keys(buttons).forEach(function (k) {
      buttons[k].disabled = busy || !current;
      buttons[k].style.background = busy || !current ? "#8a8a8a" : "#07c160";
      buttons[k].textContent = busy && busyKind === k
        ? "下载中…"
        : { video: "视频", mp3: "MP3", cover: "封面", images: "图片" }[k];
    });
  }

  async function send(kind) {
    if (busy || !current) return;
    var body;
    if (kind === "cover") {
      if (!current.cover) return;
      body = { kind: "cover", cover: current.cover, title: current.title, author: current.author };
    } else if (kind === "images") {
      if (!current.files.length && !current.bgm) {
        showTip("这个内容没有可下载的图片。", 3000);
        return;
      }
      body = {
        kind: "images", files: current.files, bgm: current.bgm,
        title: current.title, author: current.author,
      };
    } else {
      body = {
        kind: kind, url: current.url, key: current.key, spec: quality.value || "",
        title: current.title, author: current.author,
      };
    }
    busy = true;
    busyKind = kind;
    refresh();
    showTip(kind === "mp3" ? "正在下载并转换 MP3…" : "已提交下载…");
    try {
      var resp = await fetch(API, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
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
      busyKind = "";
      refresh();
    }
  }

  function mount() {
    if (!document.body) {
      setTimeout(mount, 50);
      return;
    }
    if (!panel.isConnected) document.body.appendChild(panel);
    refresh();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", mount);
  } else {
    mount();
  }
})();
