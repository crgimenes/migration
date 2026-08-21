"use strict";

const $ = (id) => document.getElementById(id);

let appInfo = { configured: false, postgres: false };

function esc(s) {
	const div = document.createElement("div");
	div.textContent = s;
	return div.innerHTML;
}

function showError(el, err) {
	el.innerHTML = `<span class="error">${esc(String(err))}</span>`;
}

function changeList(changes) {
	let html = '<ul class="changes">';
	let lastGroup = "";
	for (const c of changes) {
		const group = c.table ? `${c.schema}.${c.table}` : `${c.schema}.${c.object}`;
		if (group !== lastGroup) {
			html += `<li class="group">${esc(group)}</li>`;
			lastGroup = group;
		}
		const dropped = c.kind.endsWith("_dropped") || c.kind === "enum_altered";
		let line = c.kind + " " + c.object;
		if (c.old && c.new) {
			line += `: ${c.old} -> ${c.new}`;
		} else if (c.old || c.new) {
			line += `: ${c.old || c.new}`;
		}
		html += `<li class="change${dropped ? " dropped" : ""}">${esc(line)}</li>`;
		if (c.note) {
			html += `<li class="note">${esc(c.note)}</li>`;
		}
	}
	html += "</ul>";
	return html;
}

// --- screens -------------------------------------------------------------

const screens = ["status", "drift", "report", "connection"];

function showScreen(name) {
	for (const s of screens) {
		$("screen-" + s).hidden = s !== name;
	}
	document.querySelectorAll(".nav-item").forEach((b) => {
		if (b.dataset.screen === name) {
			b.setAttribute("aria-current", "page");
		} else {
			b.removeAttribute("aria-current");
		}
	});
	if (name === "status") {
		loadStatus();
	} else if (name === "drift") {
		loadDrift();
	} else if (name === "report") {
		loadReportControls();
	}
}

function currentScreen() {
	for (const s of screens) {
		if (!$("screen-" + s).hidden) {
			return s;
		}
	}
	return "status";
}

// --- info / sidebar ------------------------------------------------------

async function loadInfo() {
	appInfo = await window.migration_info();

	const attached = $("attached");
	if (appInfo.configured) {
		attached.classList.add("connected");
		$("attached-name").textContent = appInfo.database;
		attached.title = appInfo.database + "\n" + appInfo.dir;
	} else {
		attached.classList.remove("connected");
		$("attached-name").textContent = "not connected";
		attached.title = "";
	}
	$("app-version").textContent = appInfo.version || "";

	if ($("conn-url").value === "" && appInfo.form_url) {
		$("conn-url").value = appInfo.form_url;
	}
	if ($("conn-dir").value === "" && appInfo.form_dir) {
		$("conn-dir").value = appInfo.form_dir;
	}
	return appInfo;
}

// --- status --------------------------------------------------------------

function hideStatusConfirms() {
	$("up-confirm").hidden = true;
	$("down-confirm").hidden = true;
}

async function loadStatus() {
	hideStatusConfirms();
	$("status-result").textContent = "";
	const line = $("status-line");
	const files = $("status-files");
	files.innerHTML = "";
	if (!appInfo.configured) {
		line.innerHTML = '<span class="hint">Not connected.</span>';
		$("up-open").disabled = true;
		$("revert-open").disabled = true;
		return;
	}
	try {
		const st = await window.migration_status();
		let html = `Applied version: <strong>${st.applied}</strong>`;
		if (st.pending === 0) {
			html += ' &middot; <span class="ok">no pending migrations</span>';
		} else {
			html += ` &middot; <span class="warn">${st.pending} pending</span>`;
			for (const f of st.files) {
				files.innerHTML += `<li>${esc(f)}</li>`;
			}
		}
		line.innerHTML = html;
		$("up-open").disabled = st.pending === 0;
		$("revert-open").disabled = st.applied === 0;
	} catch (err) {
		showError(line, err);
	}
}

async function openUpConfirm() {
	hideStatusConfirms();
	try {
		const p = await window.migration_up_preview();
		$("up-confirm-text").textContent =
			`This will run ${p.files.length} migration${p.files.length === 1 ? "" : "s"}:`;
		$("up-sql").textContent = p.sql;
		$("up-confirm").hidden = false;
	} catch (err) {
		showError($("status-result"), err);
	}
}

async function runUp() {
	$("up-run").disabled = true;
	try {
		await window.migration_run_up();
		$("status-result").innerHTML = '<span class="ok">Migrations applied.</span>';
		await loadStatus();
	} catch (err) {
		showError($("status-result"), err);
		hideStatusConfirms();
	} finally {
		$("up-run").disabled = false;
	}
}

async function openDownConfirm() {
	hideStatusConfirms();
	try {
		const p = await window.migration_down_preview();
		$("down-confirm-text").textContent =
			`This will revert the last applied migration (${p.files[0]}):`;
		$("down-sql").textContent = p.sql;
		$("down-confirm").hidden = false;
	} catch (err) {
		showError($("status-result"), err);
	}
}

async function runDown() {
	$("down-run").disabled = true;
	try {
		await window.migration_run_down();
		$("status-result").innerHTML = '<span class="ok">Migration reverted.</span>';
		await loadStatus();
	} catch (err) {
		showError($("status-result"), err);
		hideStatusConfirms();
	} finally {
		$("down-run").disabled = false;
	}
}

// --- drift ---------------------------------------------------------------

let capturePreview = null;

async function loadDrift() {
	$("capture-confirm").hidden = true;
	$("drift-result").textContent = "";
	const line = $("drift-line");
	const box = $("drift-changes");
	box.innerHTML = "";
	$("drift-actions").hidden = true;
	if (!appInfo.configured) {
		line.innerHTML = '<span class="hint">Not connected.</span>';
		return;
	}
	if (!appInfo.postgres) {
		line.innerHTML = '<span class="hint">Drift detection needs a PostgreSQL database.</span>';
		return;
	}
	try {
		const d = await window.migration_drift();
		if (!d.drift) {
			line.innerHTML = `<span class="ok">No drift.</span> <span class="hint">Live schema matches snapshot ${d.snapshot}.</span>`;
			return;
		}
		line.innerHTML = `<span class="warn">Drift against snapshot ${d.snapshot}:</span>`;
		box.innerHTML = changeList(d.changes) + `<div class="summary">${esc(d.summary)}</div>`;
		$("drift-actions").hidden = false;
	} catch (err) {
		showError(line, err);
	}
}

async function openCaptureConfirm() {
	try {
		capturePreview = await window.migration_capture_preview();
		if (!capturePreview.drift) {
			$("drift-result").innerHTML = '<span class="ok">No drift to capture.</span>';
			return;
		}
		updateCaptureNames();
		$("capture-confirm-text").textContent =
			"These files will be written and the version registered as applied:";
		$("capture-up-sql").textContent = capturePreview.up_sql;
		$("capture-down-sql").textContent = capturePreview.down_sql;
		$("capture-confirm").hidden = false;
	} catch (err) {
		showError($("drift-result"), err);
	}
}

function updateCaptureNames() {
	const name = $("capture-name").value.trim() || "captured_changes";
	$("capture-up-name").textContent = `NNN_${name}.up.sql`;
	$("capture-down-name").textContent = `NNN_${name}.down.sql`;
}

async function runCapture() {
	$("capture-run").disabled = true;
	try {
		const out = await window.migration_capture($("capture-name").value.trim());
		$("drift-result").innerHTML =
			`<span class="ok">Captured as version ${out.version}.</span> ` +
			`<span class="mono">${esc(out.up_file)}</span> - review before committing.`;
		await loadDrift();
	} catch (err) {
		showError($("drift-result"), err);
	} finally {
		$("capture-run").disabled = false;
	}
}

// --- report --------------------------------------------------------------

async function loadReportControls() {
	$("report-body").innerHTML = "";
	const from = $("report-from");
	const to = $("report-to");
	from.innerHTML = "";
	to.innerHTML = "";
	if (!appInfo.configured) {
		$("report-body").innerHTML = '<span class="hint">Not connected.</span>';
		return;
	}
	try {
		const versions = await window.migration_snapshots();
		for (const v of versions) {
			from.add(new Option(`snapshot ${v}`, String(v)));
			to.add(new Option(`snapshot ${v}`, String(v)));
		}
		if (appInfo.postgres) {
			from.add(new Option("live database", "live"));
			to.add(new Option("live database", "live"));
		}
		if (from.options.length > 0) {
			from.selectedIndex = 0;
			to.selectedIndex = to.options.length - 1;
		} else {
			$("report-body").innerHTML =
				'<span class="hint">No snapshots yet. Run migrations first.</span>';
		}
	} catch (err) {
		showError($("report-body"), err);
	}
}

async function runReport() {
	const el = $("report-body");
	const from = $("report-from").value;
	const to = $("report-to").value;
	if (from === "" || to === "") {
		return;
	}
	el.textContent = "Comparing...";
	try {
		const r = await window.migration_report(from, to);
		if (!r.drift) {
			el.innerHTML = '<span class="ok">No differences.</span>';
			return;
		}
		el.innerHTML = changeList(r.changes) + `<div class="summary">${esc(r.summary)}</div>`;
	} catch (err) {
		showError(el, err);
	}
}

// --- connection ----------------------------------------------------------

async function connect(ev) {
	ev.preventDefault();
	const result = $("conn-result");
	const button = $("conn-connect");
	button.disabled = true;
	result.textContent = "Connecting...";
	try {
		await window.migration_connect($("conn-url").value.trim(), $("conn-dir").value.trim());
		result.innerHTML = '<span class="ok">Connected.</span>';
		await loadInfo();
		showScreen("status");
	} catch (err) {
		showError(result, err);
	} finally {
		button.disabled = false;
	}
}

// --- shell ---------------------------------------------------------------

async function refresh() {
	await loadInfo();
	showScreen(appInfo.configured ? currentScreen() : "connection");
}

window.migrationMenu = (action) => {
	if (action === "refresh") {
		refresh();
		return;
	}
	if (action.startsWith("screen:")) {
		showScreen(action.slice("screen:".length));
	}
};

function copyText(id) {
	navigator.clipboard.writeText($(id).textContent);
}

function applyZoom(pct) {
	document.documentElement.style.fontSize = pct + "%";
	localStorage.setItem("zoom", String(pct));
}

window.addEventListener("keydown", (ev) => {
	if (!(ev.metaKey || ev.ctrlKey)) {
		return;
	}
	const zoom = Number(localStorage.getItem("zoom") || "100");
	if (ev.key === "+" || ev.key === "=") {
		applyZoom(Math.min(zoom + 10, 200));
		ev.preventDefault();
	} else if (ev.key === "-") {
		applyZoom(Math.max(zoom - 10, 60));
		ev.preventDefault();
	} else if (ev.key === "0") {
		applyZoom(100);
		ev.preventDefault();
	}
});

applyZoom(Number(localStorage.getItem("zoom") || "100"));

document.querySelectorAll(".nav-item").forEach((b) => {
	b.addEventListener("click", () => showScreen(b.dataset.screen));
});

$("up-open").addEventListener("click", openUpConfirm);
$("up-cancel").addEventListener("click", hideStatusConfirms);
$("up-copy").addEventListener("click", () => copyText("up-sql"));
$("up-run").addEventListener("click", runUp);

$("revert-open").addEventListener("click", openDownConfirm);
$("down-cancel").addEventListener("click", hideStatusConfirms);
$("down-copy").addEventListener("click", () => copyText("down-sql"));
$("down-run").addEventListener("click", runDown);

$("capture-open").addEventListener("click", openCaptureConfirm);
$("capture-cancel").addEventListener("click", () => {
	$("capture-confirm").hidden = true;
});
$("capture-copy").addEventListener("click", () => {
	navigator.clipboard.writeText(
		$("capture-up-sql").textContent + "\n" + $("capture-down-sql").textContent);
});
$("capture-run").addEventListener("click", runCapture);
$("capture-name").addEventListener("input", updateCaptureNames);

$("report-run").addEventListener("click", runReport);
$("conn-form").addEventListener("submit", connect);
$("conn-choose").addEventListener("click", async () => {
	try {
		const dir = await window.migration_pick_directory($("conn-dir").value.trim());
		if (dir) {
			$("conn-dir").value = dir;
		}
	} catch (err) {
		showError($("conn-result"), err);
	}
});

refresh();
