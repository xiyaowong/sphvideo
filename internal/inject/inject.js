(function () {
  if (window.__sphInstalled) return;
  window.__sphInstalled = true;

  var BASE = "http://127.0.0.1:__SPH_PORT__/__sph/";
  var API = BASE + "download";
  var JOB = BASE + "job?id=";
  var CANCEL = BASE + "cancel";
  var REVEAL = BASE + "reveal";
  var PING = BASE + "ping";

  var current = null;
  var live = false;
  var active = null;
  var lastId = "";
  var connected = false;
  var pingMisses = 0;
  var pollMisses = 0;
  var collapsed = false;

  try {
    collapsed = localStorage.getItem("sph.collapsed") === "1";
  } catch (e) {}

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
    if (isFinite(w) && isFinite(h) && w > 0 && h > 0) {
      var shortSide = Math.min(w, h);
      var res = shortSide >= 1080 ? "1080P" : shortSide >= 720 ? "720P" : shortSide >= 480 ? "480P" : "";
      if (res) parts.push(res);
      parts.push(w + "×" + h);
    }
    if (spec.fileFormat) parts.push(String(spec.fileFormat).toUpperCase());
    return parts.join(" · ") || "其他画质";
  }

  function basename(path) {
    return String(path || "").split(/[\\/]/).pop() || "文件";
  }

  function onFeed(feed) {
    live = false;
    if (!feed) {
      current = null;
      render();
      return;
    }
    if (feed.liveInfo) {
      current = null;
      live = true;
      render();
      return;
    }
    var d = feed.objectDesc;
    if (!d) {
      current = null;
      render();
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
    }
    render();
  }
  window.__sphOnFeed = onFeed;

  var panel = document.createElement("div");
  panel.style.cssText = [
    "position:fixed", "right:24px", "bottom:24px", "z-index:2147483647",
    "display:flex", "flex-direction:column", "gap:8px", "width:288px",
    "padding:10px", "border-radius:12px",
    "background:rgba(255,255,255,.97)", "box-shadow:0 6px 24px rgba(0,0,0,.22)",
    "font:13px/1.4 system-ui,-apple-system,sans-serif", "color:#222",
  ].join(";");

  var header = document.createElement("div");
  header.style.cssText = "display:flex;align-items:center;gap:6px;";
  panel.appendChild(header);

  var brand = document.createElement("span");
  brand.textContent = "sphvideo";
  brand.title = "由 sphvideo 提供，非微信官方功能";
  brand.style.cssText = "font-weight:600;font-size:12px;color:#333;";
  header.appendChild(brand);

  var dot = document.createElement("span");
  dot.style.cssText = "width:7px;height:7px;border-radius:50%;background:#bbb;";
  header.appendChild(dot);

  var connText = document.createElement("span");
  connText.textContent = "连接中…";
  connText.style.cssText = "font-size:11px;color:#777;";
  header.appendChild(connText);

  var collapseBtn = document.createElement("button");
  collapseBtn.type = "button";
  collapseBtn.textContent = "−";
  collapseBtn.title = "收起面板";
  collapseBtn.style.cssText = [
    "margin-left:auto", "padding:0 4px", "border:0", "background:transparent",
    "color:#666", "font:16px/1 system-ui,sans-serif", "cursor:pointer",
  ].join(";");
  collapseBtn.onclick = function () {
    collapsed = !collapsed;
    try {
      localStorage.setItem("sph.collapsed", collapsed ? "1" : "0");
    } catch (e) {}
    render();
  };
  header.appendChild(collapseBtn);

  var body = document.createElement("div");
  body.style.cssText = "display:flex;flex-direction:column;gap:8px;";
  panel.appendChild(body);

  var hint = document.createElement("div");
  hint.textContent = "等待视频号内容…";
  hint.style.cssText = "color:#666;font-size:12px;";
  body.appendChild(hint);

  var quality = document.createElement("select");
  quality.style.cssText = [
    "width:100%", "padding:6px 8px", "border:1px solid #d0d0d0",
    "border-radius:6px", "background:#fff", "color:#222",
    "font:12px/1.2 system-ui,sans-serif", "outline:none",
  ].join(";");
  body.appendChild(quality);

  var buttonRow = document.createElement("div");
  buttonRow.style.cssText = "display:flex;gap:6px;flex-wrap:wrap;";
  body.appendChild(buttonRow);

  var buttons = {};
  var labels = { video: "下载视频", cover: "仅封面", images: "下载图片" };
  var tips = {
    video: "按上方画质下载当前视频",
    cover: "只下载封面图片",
    images: "把全部图片打包成 ZIP",
  };
  ["video", "cover", "images"].forEach(function (kind) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = labels[kind];
    b.setAttribute("data-tip", tips[kind]);
    b.style.cssText = [
      "flex:1 1 auto", "min-width:78px", "padding:8px 10px",
      "border:0", "border-radius:8px", "background:#07c160", "color:#fff",
      "font:600 12px/1.2 system-ui,sans-serif", "cursor:pointer",
      "white-space:nowrap",
    ].join(";");
    b.onclick = function () { send(kind); };
    buttons[kind] = b;
    buttonRow.appendChild(b);
  });

  var progressWrap = document.createElement("div");
  progressWrap.style.cssText = "display:none;flex-direction:column;gap:6px;";
  body.appendChild(progressWrap);

  var barOuter = document.createElement("div");
  barOuter.style.cssText = "height:6px;background:#e8e8e8;border-radius:999px;overflow:hidden;";
  progressWrap.appendChild(barOuter);

  var barInner = document.createElement("div");
  barInner.style.cssText = "height:100%;width:0;background:#07c160;transition:width .2s;";
  barOuter.appendChild(barInner);

  var progressRow = document.createElement("div");
  progressRow.style.cssText = "display:flex;align-items:center;gap:8px;";
  progressWrap.appendChild(progressRow);

  var progressText = document.createElement("span");
  progressText.style.cssText = "flex:1;font-size:11px;color:#555;min-width:0;";
  progressRow.appendChild(progressText);

  var cancelBtn = document.createElement("button");
  cancelBtn.type = "button";
  cancelBtn.textContent = "取消下载";
  cancelBtn.style.cssText = [
    "padding:0", "border:0", "background:transparent", "color:#576b95",
    "font:12px/1.2 system-ui,sans-serif", "cursor:pointer", "white-space:nowrap",
  ].join(";");
  cancelBtn.onclick = cancelDownload;
  progressRow.appendChild(cancelBtn);

  var result = document.createElement("div");
  result.style.cssText = "display:none;align-items:center;gap:8px;padding:6px 8px;border-radius:8px;background:#f5f5f5;font-size:12px;color:#333;";
  body.appendChild(result);

  var resultText = document.createElement("span");
  resultText.style.cssText = "flex:1;min-width:0;word-break:break-all;";
  result.appendChild(resultText);

  var openBtn = document.createElement("button");
  openBtn.type = "button";
  openBtn.textContent = "打开文件夹";
  openBtn.style.cssText = [
    "padding:0", "border:0", "background:transparent", "color:#576b95",
    "font:12px/1.2 system-ui,sans-serif", "cursor:pointer", "white-space:nowrap",
  ].join(";");
  openBtn.onclick = openFolder;
  result.appendChild(openBtn);

  function setConnected(ok) {
    if (connected === ok) return;
    connected = ok;
    if (!ok) {
      pingMisses = 0;
    }
    render();
  }

  function render() {
    var type = current && current.type;

    body.style.display = collapsed ? "none" : "flex";
    collapseBtn.textContent = collapsed ? "+" : "−";
    collapseBtn.title = collapsed ? "展开面板" : "收起面板";

    var running = !!active && active.state === "running";
    var pct = active && active.progress >= 0 ? Math.round(active.progress * 100) : -1;
    if (!connected) {
      dot.style.background = "#d93025";
      connText.textContent = collapsed ? "已断开" : "已断开";
    } else if (running) {
      dot.style.background = "#07c160";
      connText.textContent = collapsed ? (pct >= 0 ? "下载中 " + pct + "%" : "下载中…") : "已连接";
    } else {
      dot.style.background = "#07c160";
      connText.textContent = "已连接";
    }

    quality.style.display = type === "video" && !running ? "" : "none";
    if (type === "video" && !running) {
      var prev = quality.value;
      quality.innerHTML = "";
      var opt = document.createElement("option");
      opt.value = "";
      opt.textContent = "原画（推荐）";
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

    var canVideo = connected && type === "video" && !running && !!(current && current.url);
    var canCover = connected && !running && !!(current && current.cover);
    var canImages = connected && type === "images" && !running && !!(current && (current.files.length || current.bgm));
    setButton(buttons.video, canVideo, "当前内容不是视频");
    setButton(buttons.cover, canCover, current ? "当前内容没有封面" : "等待视频号内容");
    setButton(buttons.images, canImages, type === "images" ? "这个内容没有可下载的图片" : "当前内容不是图片合集");

    if (running) {
      progressWrap.style.display = "flex";
      barOuter.style.display = active.progress >= 0 ? "" : "none";
      barInner.style.width = pct >= 0 ? pct + "%" : "0";
      var text = active.detail || "正在下载…";
      if (pct >= 0) text += "  " + pct + "%";
      progressText.textContent = text;
    } else {
      progressWrap.style.display = "none";
    }

    if (running) {
      hint.style.display = "";
      hint.textContent = "可以切换到其他视频，下载会继续。";
    } else {
      hint.style.display = "";
      if (!connected) {
        hint.textContent = "下载服务已断开，请回到软件窗口点「启动」。";
      } else if (live) {
        hint.textContent = "直播暂不支持下载。";
      } else if (!current) {
        hint.textContent = "等待视频号内容…";
      } else if (type === "images") {
        hint.textContent = "共 " + current.files.length + " 张图片" + (current.bgm ? "，含背景音乐" : "") + "。";
      } else {
        hint.textContent = "选择画质后点「下载视频」。";
      }
    }
  }

  function setButton(b, enabled, reason) {
    b.disabled = !enabled;
    b.style.background = enabled ? "#07c160" : "#c9c9c9";
    b.style.cursor = enabled ? "pointer" : "default";
    if (enabled) {
      b.title = b.getAttribute("data-tip") || "";
    } else if (!connected) {
      b.title = "下载服务未连接";
    } else if (active && active.state === "running") {
      b.title = "请先等待当前下载完成";
    } else {
      b.title = reason;
    }
  }

  function showResult(text, isError, canOpen) {
    result.style.display = "flex";
    result.style.background = isError ? "#fdecea" : "#f5f5f5";
    resultText.style.color = isError ? "#b3261e" : "#333";
    resultText.textContent = text;
    openBtn.style.display = canOpen ? "" : "none";
  }

  function hideResult() {
    result.style.display = "none";
  }

  async function send(kind) {
    if (active || !current || !connected) return;
    var payload;
    if (kind === "cover") {
      if (!current.cover) return;
      payload = { kind: "cover", cover: current.cover, title: current.title, author: current.author };
    } else if (kind === "images") {
      if (!current.files.length && !current.bgm) {
        showResult("这个内容没有可下载的图片。", true, false);
        return;
      }
      payload = {
        kind: "images", files: current.files, bgm: current.bgm,
        title: current.title, author: current.author,
      };
    } else {
      payload = {
        kind: "video", url: current.url, key: current.key, spec: quality.value || "",
        title: current.title, author: current.author,
      };
    }
    hideResult();
    try {
      var resp = await fetch(API, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      var data = await resp.json();
      if (!data.ok) {
        showResult("下载失败：" + (data.error || "请重试"), true, false);
        return;
      }
      active = { id: data.id, state: "running", progress: 0, detail: "准备中…" };
      pollMisses = 0;
      render();
      poll();
    } catch (e) {
      setConnected(false);
      showResult("下载服务已断开。请回到软件窗口点「启动」。", true, false);
    }
  }

  function poll() {
    if (!active) return;
    var id = active.id;
    fetch(JOB + encodeURIComponent(id), { cache: "no-store" })
      .then(function (resp) {
        if (!resp.ok) throw new Error("status " + resp.status);
        return resp.json();
      })
      .then(function (data) {
        if (!data.ok) throw new Error(data.error || "读取进度失败");
        if (!active || active.id !== id) return;
        var job = data.job;
        active = job;
        if (job.state === "running") {
          pollMisses = 0;
          render();
          setTimeout(poll, 800);
        } else {
          finish(job);
        }
      })
      .catch(function () {
        if (!active || active.id !== id) return;
        pollMisses++;
        if (pollMisses >= 3) {
          active = null;
          setConnected(false);
          showResult("读取进度失败。请回到软件窗口确认下载服务。", true, false);
          render();
          return;
        }
        setTimeout(poll, 1200);
      });
  }

  function finish(job) {
    active = null;
    render();
    if (job.state === "done") {
      lastId = job.id;
      showResult("已保存：" + basename(job.path), false, true);
    } else if (job.state === "canceled") {
      showResult("下载已取消。", false, false);
    } else {
      showResult("下载失败：" + (job.error || "请重试"), true, false);
    }
  }

  function cancelDownload() {
    if (!active) return;
    var id = active.id;
    fetch(CANCEL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: id }),
    })
      .then(function (resp) { return resp.json(); })
      .then(function (data) {
        if (!data.ok) throw new Error(data.error || "");
        if (active && active.id === id) {
          active.detail = "正在取消…";
          render();
        }
      })
      .catch(function () {
        showResult("无法取消下载，请稍后重试。", true, false);
      });
  }

  function openFolder() {
    if (!lastId) return;
    fetch(REVEAL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: lastId }),
    })
      .then(function (resp) { return resp.json(); })
      .then(function (data) {
        if (!data.ok) throw new Error(data.error || "");
      })
      .catch(function () {
        showResult("无法打开文件夹，请到软件窗口点「打开」。", true, false);
      });
  }

  function ping() {
    fetch(PING + (current ? "?feed=1" : "?feed=0"), { cache: "no-store" })
      .then(function (resp) {
        if (!resp.ok) throw new Error("status " + resp.status);
        return resp.json();
      })
      .then(function () {
        pingMisses = 0;
        setConnected(true);
      })
      .catch(function () {
        pingMisses++;
        if (pingMisses >= 3) setConnected(false);
      });
  }

  function mount() {
    if (!document.body) {
      setTimeout(mount, 50);
      return;
    }
    if (!panel.isConnected) document.body.appendChild(panel);
    render();
    ping();
    setInterval(ping, 5000);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", mount);
  } else {
    mount();
  }
})();
