"use strict";

const $ = (id) => document.getElementById(id);

function esc(s) {
	const div = document.createElement("div");
	div.textContent = s;
	return div.innerHTML;
}

function showError(el, err) {
	el.innerHTML = `<div class="error">${esc(String(err))}</div>`;
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
		html += `<li class="change${dropped ? " dropped" : ""}">${esc(line)}`;
		if (c.note) {
			html += `<div class="note">${esc(c.note)}</div>`;
		}
		html += "</li>";
	}
	html += "</ul>";
	return html;
}

async function loadInfo() {
	const info = await window.migration_info();
	$("db-target").textContent = info.database;
	$("dir-target").textContent = info.dir;
	return info;
}

async function loadStatus() {
	const el = $("status-body");
	try {
		const st = await window.migration_status();
		let html = `<span>Applied version: <strong>${st.applied}</strong></span>`;
		if (st.pending === 0) {
			html += ' &middot; <span class="ok">no pending migrations</span>';
		} else {
			html += ` &middot; <span class="warn">${st.pending} pending</span>`;
			html += '<ul class="files">';
			for (const f of st.files) {
				html += `<li class="mono">${esc(f)}</li>`;
			}
			html += "</ul>";
			html += `<div class="command"><span>Apply with:</span><code>migration up</code></div>`;
		}
		el.innerHTML = html;
	} catch (err) {
		showError(el, err);
	}
}

async function loadDrift(postgres) {
	const el = $("drift-body");
	if (!postgres) {
		el.innerHTML = '<span class="note">Drift detection needs a PostgreSQL database.</span>';
		return;
	}
	try {
		const d = await window.migration_drift();
		if (!d.drift) {
			el.innerHTML = `<span class="ok">No drift.</span> <span class="note">Live schema matches snapshot ${d.snapshot}.</span>`;
			return;
		}
		let html = `<span class="warn">Drift against snapshot ${d.snapshot}:</span>`;
		html += changeList(d.changes);
		html += `<div class="summary">${esc(d.summary)}</div>`;
		html += `<div class="command"><span>Capture with:</span><code id="capture-cmd">${esc(d.command)}</code>` +
			'<button id="copy-cmd">Copy command</button></div>';
		el.innerHTML = html;
		$("copy-cmd").addEventListener("click", () => {
			navigator.clipboard.writeText($("capture-cmd").textContent);
		});
	} catch (err) {
		showError(el, err);
	}
}

async function loadReportControls(postgres) {
	try {
		const versions = await window.migration_snapshots();
		const from = $("report-from");
		const to = $("report-to");
		from.innerHTML = "";
		to.innerHTML = "";
		for (const v of versions) {
			from.add(new Option(`snapshot ${v}`, String(v)));
			to.add(new Option(`snapshot ${v}`, String(v)));
		}
		if (postgres) {
			from.add(new Option("live database", "live"));
			to.add(new Option("live database", "live"));
		}
		if (from.options.length > 0) {
			from.selectedIndex = 0;
			to.selectedIndex = to.options.length - 1;
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
		el.innerHTML = '<span class="note">No snapshots yet. Run migrations or `migration snapshot` first.</span>';
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

async function refresh() {
	try {
		const info = await loadInfo();
		await Promise.all([loadStatus(), loadDrift(info.postgres), loadReportControls(info.postgres)]);
	} catch (err) {
		showError($("status-body"), err);
	}
}

// Menu entry point (Cmd+R routes here from the native menu).
window.migrationMenu = (action) => {
	if (action === "refresh") {
		refresh();
	}
};

// Keyboard zoom: Cmd/Ctrl +/-/0 scales the root font size, persisted.
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
$("refresh").addEventListener("click", refresh);
$("report-run").addEventListener("click", runReport);
refresh();
