// Fills in the latest APK download link and the GitHub star count from the public
// GitHub API. Everything works without it: links fall back to the Releases page.
(function () {
  var REPO = 'Sirtheprogrammer/httpsms';
  var API = 'https://api.github.com/repos/' + REPO;
  var CACHE_MS = 10 * 60 * 1000; // unauthenticated API allows 60 requests/hour per IP

  function cached(key, url) {
    try {
      var hit = JSON.parse(sessionStorage.getItem(key) || 'null');
      if (hit && Date.now() - hit.t < CACHE_MS) return Promise.resolve(hit.v);
    } catch (e) { /* storage unavailable */ }
    return fetch(url, { headers: { Accept: 'application/vnd.github+json' } }).then(function (r) {
      if (!r.ok) throw new Error('GitHub API ' + r.status);
      return r.json();
    }).then(function (v) {
      try { sessionStorage.setItem(key, JSON.stringify({ t: Date.now(), v: v })); } catch (e) { /* ignore */ }
      return v;
    });
  }

  function each(sel, fn) {
    Array.prototype.forEach.call(document.querySelectorAll(sel), fn);
  }

  function compact(n) {
    if (n >= 1000) return (n / 1000).toFixed(n >= 10000 ? 0 : 1).replace(/\.0$/, '') + 'k';
    return String(n);
  }

  cached('httpsms_repo', API).then(function (repo) {
    each('.js-stars', function (el) { el.textContent = compact(repo.stargazers_count || 0); });
  }).catch(function () {
    each('.js-stars', function (el) { el.parentNode && el.parentNode.classList.add('no-count'); el.textContent = ''; });
  });

  cached('httpsms_release', API + '/releases/latest').then(function (rel) {
    var apk = (rel.assets || []).filter(function (a) { return /\.apk$/i.test(a.name); })[0];
    if (!apk) return;
    var version = (rel.tag_name || '').replace(/^apk-v/, '').replace(/-build\.\d+$/, '');
    var size = (apk.size / 1048576).toFixed(1) + ' MB';
    each('.js-download', function (el) {
      el.href = apk.browser_download_url;
      el.setAttribute('download', apk.name);
    });
    each('.js-download-label', function (el) { el.textContent = 'Download APK v' + version; });
    each('.js-download-meta', function (el) {
      var date = new Date(rel.published_at).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
      el.textContent = 'v' + version + ' · ' + size + ' · released ' + date + ' · Android 8.0+';
    });
    each('.js-pill-text', function (el) { el.textContent = 'v' + version + ' is out · see what’s new'; });
  }).catch(function () { /* keep the Releases page fallback */ });
})();
