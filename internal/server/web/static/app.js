// Progressive enhancement for Hypnogaze mobile-friendly feed:
// 1. Tag search autocomplete with keyboard navigation & AbortController
// 2. Tag chip toggle (add/remove) & active tag badges
// 3. Interactive media lightbox with 0ms progressive thumbnail preview and background scroll locking
// 4. Post detail progressive preloading & inline HD toggle

document.documentElement.classList.add("js");

// Topbar scroll-reveal & dynamic height tracking
const topbar = document.querySelector(".topbar");

function updateTopbarHeight() {
	if (!topbar) return;
	const h = topbar.offsetHeight || 55;
	document.documentElement.style.setProperty("--topbar-height", `${h}px`);
}

updateTopbarHeight();
window.addEventListener("resize", updateTopbarHeight, { passive: true });

let lastScrollY = window.scrollY;

window.addEventListener("scroll", () => {
	const scrollY = Math.max(0, window.scrollY);
	const topbarHeight = topbar?.offsetHeight || 55;

	if (document.body.classList.contains("filter-open") || document.getElementById("mobile-filter-state")?.checked) {
		document.body.classList.remove("topbar-hidden");
		lastScrollY = scrollY;
		return;
	}

	if (scrollY <= 0) {
		document.body.classList.remove("topbar-hidden");
		document.body.classList.remove("topbar-overlay");
	} else if (scrollY > lastScrollY && scrollY > topbarHeight) {
		// Scrolling down
		document.body.classList.add("topbar-hidden");
		document.body.classList.remove("topbar-overlay");
	} else if (scrollY < lastScrollY - 2) {
		// Scrolling up
		document.body.classList.remove("topbar-hidden");
		if (scrollY > 10) {
			document.body.classList.add("topbar-overlay");
		} else {
			document.body.classList.remove("topbar-overlay");
		}
	}

	lastScrollY = scrollY;
}, { passive: true });

// Settings & Preferences Management
const DEFAULT_SETTINGS = {
	version: 1,
	theme: "system",
	viewMode: "grid",
	maxColumns: 5,
	minColumnWidth: 210,
	zoomMode: "fill-width",
	defaultWindow: {
		browse: "all",
		popular: "24h"
	},
	defaultSort: {
		browse: "new",
		popular: "hot"
	},
	defaultRating: "",
	blockedTags: [],
	preferHD: true,
	singleAutoplay: true
};

function loadSettings() {
	try {
		const raw = localStorage.getItem("hypnogaze.settings") || localStorage.getItem("hypnoview.settings");
		if (!raw) return { ...DEFAULT_SETTINGS };
		const parsed = JSON.parse(raw);
		return {
			...DEFAULT_SETTINGS,
			...parsed,
			defaultWindow: { ...DEFAULT_SETTINGS.defaultWindow, ...(parsed.defaultWindow || {}) },
			defaultSort: { ...DEFAULT_SETTINGS.defaultSort, ...(parsed.defaultSort || {}) },
			blockedTags: Array.isArray(parsed.blockedTags) ? parsed.blockedTags : []
		};
	} catch (_) {
		return { ...DEFAULT_SETTINGS };
	}
}

let currentSettings = loadSettings();

function syncCookie(s) {
	const cookieObj = {
		wb: s.defaultWindow?.browse || "all",
		wp: s.defaultWindow?.popular || "24h",
		sb: s.defaultSort?.browse || "new",
		sp: s.defaultSort?.popular || "hot",
		r: s.defaultRating || "",
		bt: Array.isArray(s.blockedTags) ? s.blockedTags : []
	};
	const encoded = encodeURIComponent(JSON.stringify(cookieObj));
	document.cookie = `hv_prefs=${encoded}; path=/; max-age=31536000; SameSite=Lax`;
}

let singleColumnObserver = null;

function updateSingleColumnAutoplay() {
	if (singleColumnObserver) {
		singleColumnObserver.disconnect();
		singleColumnObserver = null;
	}

	const isSingle = document.documentElement.dataset.viewMode === "single";
	if (!isSingle || !currentSettings.singleAutoplay) {
		document.querySelectorAll(".post-card .card-media-link video.inline-autoplay-video").forEach((vid) => {
			vid.pause();
			vid.remove();
		});
		document.querySelectorAll(".post-card[data-ext='gif'] .card-media-link img").forEach((img) => {
			const card = img.closest(".post-card");
			if (card && card.dataset.sampleUrl) {
				img.src = card.dataset.sampleUrl;
			}
		});
		return;
	}

	singleColumnObserver = new IntersectionObserver((entries) => {
		entries.forEach((entry) => {
			const card = entry.target;
			const ext = (card.dataset.ext || "").toLowerCase();
			const mediaLink = card.querySelector(".card-media-link");
			if (!mediaLink) return;

			if (entry.isIntersecting) {
				if (ext === "mp4" || ext === "webm") {
					let video = mediaLink.querySelector("video.inline-autoplay-video");
					if (!video) {
						video = document.createElement("video");
						video.className = "inline-autoplay-video";
						video.src = card.dataset.fileUrl;
						video.controls = true;
						video.loop = true;
						video.muted = true;
						video.playsInline = true;
						video.setAttribute("playsinline", "");
						video.setAttribute("webkit-playsinline", "");
						mediaLink.appendChild(video);
						video.play().catch(() => {});
					} else {
						video.play().catch(() => {});
					}
				} else if (ext === "gif") {
					const img = mediaLink.querySelector("img");
					if (img && card.dataset.fileUrl && img.src !== card.dataset.fileUrl) {
						img.src = card.dataset.fileUrl;
					}
				}
			} else {
				if (ext === "mp4" || ext === "webm") {
					const video = mediaLink.querySelector("video.inline-autoplay-video");
					if (video) {
						video.pause();
					}
				}
			}
		});
	}, {
		rootMargin: "300px 0px"
	});

	document.querySelectorAll(".post-card").forEach((card) => {
		singleColumnObserver.observe(card);
	});
}

function applyVisualSettings(s) {
	const el = document.documentElement;
	if (s.theme === "light" || s.theme === "dark") {
		el.dataset.theme = s.theme;
	} else {
		delete el.dataset.theme;
	}

	el.dataset.viewMode = s.viewMode === "single" ? "single" : "grid";

	const maxCols = parseInt(s.maxColumns, 10);
	if (maxCols >= 1 && maxCols <= 8) {
		el.style.setProperty("--grid-max-cols", String(maxCols));
	}

	const minW = parseInt(s.minColumnWidth, 10);
	if (minW >= 150 && minW <= 380) {
		el.style.setProperty("--grid-min-width", `${minW}px`);
	}

	updateSingleColumnAutoplay();
}

function saveSettings(s) {
	currentSettings = s;
	localStorage.setItem("hypnogaze.settings", JSON.stringify(s));
	localStorage.setItem("hypnoview.settings", JSON.stringify(s));
	syncCookie(s);
	applyVisualSettings(s);
}

// Initial sync to ensure cookie matches localStorage
if (!document.cookie.includes("hv_prefs=")) {
	syncCookie(currentSettings);
}
applyVisualSettings(currentSettings);

// View Mode 1-click Toggle in Topbar
const viewModeToggle = document.getElementById("view-mode-toggle");
if (viewModeToggle) {
	viewModeToggle.addEventListener("click", () => {
		currentSettings.viewMode = currentSettings.viewMode === "single" ? "grid" : "single";
		saveSettings(currentSettings);
		syncSettingsDialogUI();
	});
}

// Settings Dialog Elements & Logic
const settingsDialog = document.getElementById("settings-dialog");
const settingsToggle = document.getElementById("settings-toggle");
const settingsCloseBtn = document.getElementById("settings-close-btn");
const settingsDoneBtn = document.getElementById("settings-done-btn");
const settingsResetBtn = document.getElementById("settings-reset-btn");
const blockedTagsBtn = document.getElementById("blocked-tags-btn");

let serverSnapshot = null;

function getServerPrefsSnapshot() {
	return JSON.stringify({
		wb: currentSettings.defaultWindow?.browse,
		wp: currentSettings.defaultWindow?.popular,
		sb: currentSettings.defaultSort?.browse,
		sp: currentSettings.defaultSort?.popular,
		r: currentSettings.defaultRating,
		bt: currentSettings.blockedTags
	});
}

function openSettingsDialog(focusTarget) {
	if (!settingsDialog) return;
	serverSnapshot = getServerPrefsSnapshot();
	syncSettingsDialogUI();
	settingsDialog.showModal();
	if (focusTarget) {
		setTimeout(() => focusTarget.focus(), 50);
	}
}

function closeSettingsDialog() {
	if (!settingsDialog || !settingsDialog.open) return;
	settingsDialog.close();
}

if (settingsDialog) {
	settingsDialog.addEventListener("close", () => {
		const currentSnapshot = getServerPrefsSnapshot();
		if (serverSnapshot !== null && serverSnapshot !== currentSnapshot) {
			window.location.reload();
		}
	});

	settingsDialog.addEventListener("click", (e) => {
		if (e.target === settingsDialog) {
			closeSettingsDialog();
		}
	});
}

settingsToggle?.addEventListener("click", () => openSettingsDialog());
settingsCloseBtn?.addEventListener("click", closeSettingsDialog);
settingsDoneBtn?.addEventListener("click", closeSettingsDialog);
blockedTagsBtn?.addEventListener("click", () => openSettingsDialog(document.getElementById("blocklist-input")));

function setSegmentedActive(containerId, value) {
	const container = document.getElementById(containerId);
	if (!container) return;
	container.querySelectorAll(".segmented-btn").forEach((btn) => {
		const matches = btn.dataset.value === value;
		btn.classList.toggle("active", matches);
		btn.setAttribute("aria-checked", matches ? "true" : "false");
	});
}

function renderBlocklistChips() {
	const chipsContainer = document.getElementById("blocklist-chips");
	if (!chipsContainer) return;
	chipsContainer.innerHTML = "";
	(currentSettings.blockedTags || []).forEach((tag) => {
		const chip = document.createElement("span");
		chip.className = "blocklist-chip";
		chip.innerHTML = `<span class="blocklist-chip-tag">${tag}</span><button type="button" class="blocklist-chip-remove" aria-label="Remove blocked tag ${tag}"><span class="material-symbols-rounded" aria-hidden="true">close</span></button>`;
		chip.querySelector(".blocklist-chip-remove").addEventListener("click", () => {
			currentSettings.blockedTags = currentSettings.blockedTags.filter((t) => t !== tag);
			saveSettings(currentSettings);
			renderBlocklistChips();
			updateSettingsJsonTextarea();
		});
		chipsContainer.appendChild(chip);
	});
}

function updateSettingsJsonTextarea() {
	const ta = document.getElementById("settings-json-textarea");
	if (ta) {
		ta.value = JSON.stringify(currentSettings, null, 2);
	}
}

function syncSettingsDialogUI() {
	setSegmentedActive("setting-theme", currentSettings.theme);
	setSegmentedActive("setting-view-mode", currentSettings.viewMode);

	const maxColsInput = document.getElementById("setting-max-cols");
	const maxColsVal = document.getElementById("setting-max-cols-val");
	if (maxColsInput) maxColsInput.value = currentSettings.maxColumns;
	if (maxColsVal) maxColsVal.textContent = `${currentSettings.maxColumns} cols`;

	const minWidthInput = document.getElementById("setting-min-width");
	const minWidthVal = document.getElementById("setting-min-width-val");
	if (minWidthInput) minWidthInput.value = currentSettings.minColumnWidth;
	if (minWidthVal) minWidthVal.textContent = `${currentSettings.minColumnWidth}px`;

	setSegmentedActive("setting-window-popular", currentSettings.defaultWindow?.popular || "24h");
	setSegmentedActive("setting-window-browse", currentSettings.defaultWindow?.browse || "all");
	setSegmentedActive("setting-default-rating", currentSettings.defaultRating || "");
	setSegmentedActive("setting-zoom-mode", currentSettings.zoomMode || "fill-width");

	const preferHdInput = document.getElementById("setting-prefer-hd");
	if (preferHdInput) preferHdInput.checked = !!currentSettings.preferHD;

	const singleAutoplayInput = document.getElementById("setting-single-autoplay");
	if (singleAutoplayInput) singleAutoplayInput.checked = !!currentSettings.singleAutoplay;

	renderBlocklistChips();
	updateSettingsJsonTextarea();
}

// Dialog Interactive Controls
document.querySelectorAll("#setting-theme .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.theme = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-theme", currentSettings.theme);
		updateSettingsJsonTextarea();
	});
});

document.querySelectorAll("#setting-view-mode .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.viewMode = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-view-mode", currentSettings.viewMode);
		updateSettingsJsonTextarea();
	});
});

const settingMaxCols = document.getElementById("setting-max-cols");
settingMaxCols?.addEventListener("input", () => {
	const val = parseInt(settingMaxCols.value, 10);
	currentSettings.maxColumns = val;
	const valBadge = document.getElementById("setting-max-cols-val");
	if (valBadge) valBadge.textContent = `${val} cols`;
	saveSettings(currentSettings);
	updateSettingsJsonTextarea();
});

const settingMinWidth = document.getElementById("setting-min-width");
settingMinWidth?.addEventListener("input", () => {
	const val = parseInt(settingMinWidth.value, 10);
	currentSettings.minColumnWidth = val;
	const valBadge = document.getElementById("setting-min-width-val");
	if (valBadge) valBadge.textContent = `${val}px`;
	saveSettings(currentSettings);
	updateSettingsJsonTextarea();
});

document.querySelectorAll("#setting-window-popular .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.defaultWindow.popular = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-window-popular", currentSettings.defaultWindow.popular);
		updateSettingsJsonTextarea();
	});
});

document.querySelectorAll("#setting-window-browse .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.defaultWindow.browse = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-window-browse", currentSettings.defaultWindow.browse);
		updateSettingsJsonTextarea();
	});
});

document.querySelectorAll("#setting-default-rating .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.defaultRating = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-default-rating", currentSettings.defaultRating);
		updateSettingsJsonTextarea();
	});
});

document.querySelectorAll("#setting-zoom-mode .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.zoomMode = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-zoom-mode", currentSettings.zoomMode);
		updateSettingsJsonTextarea();
	});
});

document.getElementById("setting-prefer-hd")?.addEventListener("change", (e) => {
	currentSettings.preferHD = e.target.checked;
	saveSettings(currentSettings);
	updateSettingsJsonTextarea();
});

document.getElementById("setting-single-autoplay")?.addEventListener("change", (e) => {
	currentSettings.singleAutoplay = e.target.checked;
	saveSettings(currentSettings);
	updateSettingsJsonTextarea();
});

const blocklistInput = document.getElementById("blocklist-input");
const blocklistAddBtn = document.getElementById("blocklist-add-btn");
const blocklistSuggestions = document.getElementById("blocklist-suggestions");

function addBlocklistTag(tag) {
	tag = (tag || "").toLowerCase().trim().replace(/^-+/, "");
	if (!tag) return;
	if (!currentSettings.blockedTags.includes(tag)) {
		currentSettings.blockedTags.push(tag);
		saveSettings(currentSettings);
		renderBlocklistChips();
		updateSettingsJsonTextarea();
	}
	if (blocklistInput) blocklistInput.value = "";
	if (blocklistSuggestions) {
		blocklistSuggestions.innerHTML = "";
	}
}

blocklistAddBtn?.addEventListener("click", () => {
	if (blocklistInput) addBlocklistTag(blocklistInput.value);
});

// Reusable HTMX autocomplete box keyboard navigation and selection handler
function bindAutocompleteBox(inputEl, boxEl, onSelect) {
	if (!inputEl || !boxEl) return;
	let activeIdx = -1;

	function getItems() {
		return Array.from(boxEl.querySelectorAll(".tag-suggestion-item"));
	}

	function updateActive() {
		const items = getItems();
		items.forEach((item, idx) => {
			const isActive = idx === activeIdx;
			item.classList.toggle("is-active", isActive);
			if (isActive) {
				item.scrollIntoView({ block: "nearest" });
			}
		});
	}

	boxEl.addEventListener("htmx:afterSwap", () => {
		activeIdx = -1;
	});

	inputEl.addEventListener("keydown", (e) => {
		const items = getItems();
		if (e.key === "ArrowDown") {
			if (!items.length) return;
			e.preventDefault();
			activeIdx = (activeIdx + 1) % items.length;
			updateActive();
		} else if (e.key === "ArrowUp") {
			if (!items.length) return;
			e.preventDefault();
			activeIdx = (activeIdx - 1 + items.length) % items.length;
			updateActive();
		} else if (e.key === "Enter") {
			if (activeIdx >= 0 && items[activeIdx]) {
				e.preventDefault();
				items[activeIdx].click();
			}
		} else if (e.key === "Escape") {
			e.preventDefault();
			e.stopPropagation();
			boxEl.innerHTML = "";
			activeIdx = -1;
		}
	});

	boxEl.addEventListener("click", (e) => {
		const item = e.target.closest(".tag-suggestion-item");
		if (!item) return;
		e.preventDefault();
		onSelect(item);
		boxEl.innerHTML = "";
		activeIdx = -1;
	});

	document.addEventListener("click", (e) => {
		if (!boxEl.contains(e.target) && e.target !== inputEl) {
			boxEl.innerHTML = "";
			activeIdx = -1;
		}
	});
}

bindAutocompleteBox(blocklistInput, blocklistSuggestions, (item) => {
	const tag = item.dataset.addBlockTag;
	if (tag) {
		addBlocklistTag(tag);
	}
	blocklistInput?.focus();
});

document.getElementById("settings-json-apply-btn")?.addEventListener("click", () => {
	const ta = document.getElementById("settings-json-textarea");
	if (!ta) return;
	try {
		const parsed = JSON.parse(ta.value);
		const merged = {
			...DEFAULT_SETTINGS,
			...parsed,
			defaultWindow: { ...DEFAULT_SETTINGS.defaultWindow, ...(parsed.defaultWindow || {}) },
			defaultSort: { ...DEFAULT_SETTINGS.defaultSort, ...(parsed.defaultSort || {}) },
			blockedTags: Array.isArray(parsed.blockedTags) ? parsed.blockedTags : []
		};
		saveSettings(merged);
		syncSettingsDialogUI();
	} catch (err) {
		alert("Invalid JSON: " + err.message);
	}
});

settingsResetBtn?.addEventListener("click", () => {
	if (confirm("Reset all settings & preferences to defaults?")) {
		saveSettings({ ...DEFAULT_SETTINGS });
		syncSettingsDialogUI();
	}
});

const searchInput = document.getElementById("tag-search-input");
const suggestionsBox = document.getElementById("tag-suggestions");

// Parse currently active tags in search input
function getSearchTokens() {
	if (!searchInput) return [];
	return searchInput.value.trim().split(/\s+/).filter(Boolean);
}

// Highlight and toggle quick tag chips
const currentTokens = getSearchTokens();
document.querySelectorAll(".tag-chip[data-tag]").forEach((btn) => {
	const tag = btn.dataset.tag;
	if (currentTokens.includes(tag)) {
		btn.classList.add("active");
		btn.setAttribute("aria-pressed", "true");
	} else {
		btn.setAttribute("aria-pressed", "false");
	}

	btn.addEventListener("click", (e) => {
		if (!searchInput) return;
		e.preventDefault();
		let tokens = getSearchTokens();
		if (tokens.includes(tag)) {
			tokens = tokens.filter((t) => t !== tag && t !== "-" + tag);
		} else {
			tokens.push(tag);
		}
		searchInput.value = tokens.join(" ") + (tokens.length > 0 ? " " : "");
		searchInput.form?.submit();
	});
});

// Clickable active tag badges in results summary remove tag on click
document.querySelectorAll(".active-tag[data-remove-tag]").forEach((btn) => {
	btn.addEventListener("click", (e) => {
		if (!searchInput) return;
		e.preventDefault();
		const removeTag = btn.dataset.removeTag;
		let tokens = getSearchTokens().filter((t) => t !== removeTag && t !== "-" + removeTag);
		searchInput.value = tokens.join(" ") + (tokens.length > 0 ? " " : "");
		searchInput.form?.submit();
	});
});

// Bind HTMX autocomplete for main search input
bindAutocompleteBox(searchInput, suggestionsBox, (item) => {
	const replacement = item.dataset.setValue;
	if (replacement) {
		searchInput.value = replacement;
	}
	searchInput.focus();
});

// Mobile Search & Filter Overlay
const mobileFilterCheckbox = document.getElementById("mobile-filter-state");
const mobileFilterToggle = document.getElementById("mobile-filter-toggle");
const indexSidebar = document.getElementById("index-sidebar");
const mobileFilterClose = document.getElementById("mobile-filter-close");

if (mobileFilterToggle && indexSidebar) {
	function syncDrawerState(isOpen, fromHistory = false) {
		if (mobileFilterCheckbox) {
			mobileFilterCheckbox.checked = isOpen;
		}
		if (isOpen) {
			document.body.classList.remove("topbar-hidden");
			indexSidebar.classList.add("is-open");
			document.body.classList.add("filter-open");
			mobileFilterToggle.setAttribute("aria-expanded", "true");
			if (!fromHistory) {
				window.history.pushState({ mobileFilter: true }, "");
			}
		} else {
			indexSidebar.classList.remove("is-open");
			document.body.classList.remove("filter-open");
			mobileFilterToggle.setAttribute("aria-expanded", "false");
			if (!fromHistory && window.history.state?.mobileFilter) {
				window.history.back();
			}
		}
	}

	mobileFilterToggle.addEventListener("click", (e) => {
		e.preventDefault();
		const nextState = !mobileFilterCheckbox?.checked;
		syncDrawerState(nextState);
	});

	mobileFilterClose?.addEventListener("click", (e) => {
		e.preventDefault();
		syncDrawerState(false);
	});

	// Keyboard accessibility for label buttons (Enter / Space)
	mobileFilterToggle.addEventListener("keydown", (e) => {
		if (e.key === " " || e.key === "Enter") {
			e.preventDefault();
			const nextState = !mobileFilterCheckbox?.checked;
			syncDrawerState(nextState);
		}
	});

	mobileFilterClose?.addEventListener("keydown", (e) => {
		if (e.key === " " || e.key === "Enter") {
			e.preventDefault();
			syncDrawerState(false);
		}
	});

	// Close on backdrop click (clicking outside .controls-panel)
	indexSidebar.addEventListener("click", (e) => {
		if (e.target === indexSidebar || e.target.classList.contains("mobile-sidebar-backdrop")) {
			syncDrawerState(false);
		}
	});

	// Popstate handler for mobile filter Back button
	window.addEventListener("popstate", () => {
		if (mobileFilterCheckbox?.checked || indexSidebar.classList.contains("is-open")) {
			syncDrawerState(false, true);
		}
	});

	// Escape key closes mobile filter if open
	document.addEventListener("keydown", (e) => {
		if (e.key === "Escape" && (mobileFilterCheckbox?.checked || indexSidebar.classList.contains("is-open"))) {
			e.preventDefault();
			syncDrawerState(false);
		}
	});

	// Automatically clean up mobile filter state if viewport is resized to desktop (>= 960px)
	window.addEventListener("resize", () => {
		if (window.matchMedia("(min-width: 960px)").matches && (mobileFilterCheckbox?.checked || indexSidebar.classList.contains("is-open"))) {
			syncDrawerState(false, true);
		}
	});
}

// Lightbox implementation
const cards = Array.from(document.querySelectorAll(".post-card"));
const lightbox = document.getElementById("lightbox");

if (lightbox && cards.length > 0) {
	const lbPostLink = document.getElementById("lb-post-link");
	const lbScore = document.getElementById("lb-score");
	const lbRate = document.getElementById("lb-rate");
	const lbHotVal = document.getElementById("lb-hot-val");
	const lbAge = document.getElementById("lb-age");
	const lbHdToggle = document.getElementById("lb-hd-toggle");
	const lbHypnohubLink = document.getElementById("lb-hypnohub-link");
	const lbClose = document.getElementById("lb-close");
	const lbPrev = document.getElementById("lb-prev");
	const lbNext = document.getElementById("lb-next");
	const lbMediaWrap = document.getElementById("lb-media-wrap");
	const lbTags = document.getElementById("lb-tags");
	const lbStage = document.getElementById("lightbox-stage");
	const lbZoomToggle = document.getElementById("lb-zoom-toggle");

	let currentIndex = 0;
	let preferHD = currentSettings.preferHD ?? true;
	let isZoomed = false;
	let currentZoomScale = 1;

	function getRenderedMediaRect(stage, mediaEl, postW, postH) {
		const stageRect = stage.getBoundingClientRect();
		const sw = stageRect.width;
		const sh = stageRect.height;
		const ar = (postW && postH) ? (postW / postH) : ((mediaEl?.naturalWidth && mediaEl?.naturalHeight) ? (mediaEl.naturalWidth / mediaEl.naturalHeight) : 1);
		let rw, rh, left, top;
		if (sw / sh > ar) {
			rh = sh;
			rw = sh * ar;
			top = 0;
			left = (sw - rw) / 2;
		} else {
			rw = sw;
			rh = sw / ar;
			left = 0;
			top = (sh - rh) / 2;
		}
		return {
			left: stageRect.left + left,
			top: stageRect.top + top,
			right: stageRect.left + left + rw,
			bottom: stageRect.top + top + rh,
			width: rw,
			height: rh
		};
	}

	function resetZoom() {
		isZoomed = false;
		currentZoomScale = 1;
		lbStage.classList.remove("is-zoomed", "is-dragging");
		lbStage.style.removeProperty("--zoomed-w");
		lbStage.style.removeProperty("--zoomed-h");
		if (lbZoomToggle) {
			lbZoomToggle.textContent = "Fit";
			lbZoomToggle.classList.remove("active");
		}
	}

	function toggleZoom(clickX, clickY) {
		if (isZoomed) {
			resetZoom();
			return;
		}
		const card = cards[currentIndex];
		if (!card) return;
		const ext = (card.dataset.ext || "").toLowerCase();
		if (ext === "mp4" || ext === "webm") return;

		const postW = parseInt(card.dataset.width, 10) || 1000;
		const postH = parseInt(card.dataset.height, 10) || 1000;
		const r = getRenderedMediaRect(lbStage, lbMediaWrap.querySelector("img"), postW, postH);
		const sw = lbStage.clientWidth;
		const sh = lbStage.clientHeight;
		const ar = postW / postH;

		let targetW, targetH;
		const zoomMode = currentSettings.zoomMode || "fill-width";

		if (zoomMode === "original") {
			targetW = postW;
			if (postW <= r.width) targetW = Math.round(r.width * 2);
			targetH = Math.round(targetW / ar);
		} else if (zoomMode === "2x") {
			targetW = Math.round(r.width * 2);
			targetH = Math.round(r.height * 2);
		} else if (zoomMode === "3x") {
			targetW = Math.round(r.width * 3);
			targetH = Math.round(r.height * 3);
		} else {
			// "fill-width" default: fills stage width (or at least 1.75x fit width)
			const factor = Math.max(sw / r.width, 1.75);
			targetW = Math.round(r.width * factor);
			targetH = Math.round(r.height * factor);
		}

		currentZoomScale = targetW / r.width;
		isZoomed = true;
		lbStage.classList.add("is-zoomed");
		lbStage.style.setProperty("--zoomed-w", `${targetW}px`);
		lbStage.style.setProperty("--zoomed-h", `${targetH}px`);

		if (lbZoomToggle) {
			lbZoomToggle.textContent = `${Math.round(currentZoomScale * 100)}%`;
			lbZoomToggle.classList.add("active");
		}

		if (clickX !== undefined && clickY !== undefined) {
			const u = Math.max(0, Math.min(1, (clickX - r.left) / r.width));
			const v = Math.max(0, Math.min(1, (clickY - r.top) / r.height));
			requestAnimationFrame(() => {
				lbStage.scrollLeft = (u * targetW) - (clickX - lbStage.getBoundingClientRect().left);
				lbStage.scrollTop = (v * targetH) - (clickY - lbStage.getBoundingClientRect().top);
			});
		} else {
			requestAnimationFrame(() => {
				lbStage.scrollLeft = (targetW - sw) / 2;
				lbStage.scrollTop = (targetH - sh) / 2;
			});
		}
	}

	lbZoomToggle?.addEventListener("click", () => toggleZoom());

	let isDragging = false;
	let startX = 0, startY = 0;
	let scrollStartX = 0, scrollStartY = 0;
	let didDrag = false;

	lbStage?.addEventListener("mousedown", (e) => {
		if (!isZoomed || e.button !== 0) return;
		if (e.target.closest(".lb-nav") || e.target.closest("button") || e.target.closest("a")) return;
		isDragging = true;
		didDrag = false;
		startX = e.clientX;
		startY = e.clientY;
		scrollStartX = lbStage.scrollLeft;
		scrollStartY = lbStage.scrollTop;
	});

	window.addEventListener("mousemove", (e) => {
		if (!isDragging) return;
		const dx = e.clientX - startX;
		const dy = e.clientY - startY;
		if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
			didDrag = true;
			lbStage.classList.add("is-dragging");
		}
		lbStage.scrollLeft = scrollStartX - dx;
		lbStage.scrollTop = scrollStartY - dy;
	});

	window.addEventListener("mouseup", () => {
		if (isDragging) {
			isDragging = false;
			lbStage.classList.remove("is-dragging");
		}
	});

	lbStage?.addEventListener("mousemove", (e) => {
		if (isZoomed) {
			if (!isDragging) {
				const rect = lbMediaWrap.getBoundingClientRect();
				const inMedia = e.clientX >= rect.left && e.clientX <= rect.right && e.clientY >= rect.top && e.clientY <= rect.bottom;
				lbStage.style.cursor = inMedia ? "zoom-out" : "pointer";
			}
			return;
		}
		const card = cards[currentIndex];
		if (!card) return;
		const ext = (card.dataset.ext || "").toLowerCase();
		if (ext === "mp4" || ext === "webm") {
			lbStage.style.cursor = "default";
			return;
		}
		const r = getRenderedMediaRect(lbStage, lbMediaWrap.querySelector("img"), parseInt(card.dataset.width, 10), parseInt(card.dataset.height, 10));
		const inImage = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
		lbStage.style.cursor = inImage ? "zoom-in" : "pointer";
	});

	lbStage?.addEventListener("wheel", (e) => {
		if (!lightbox.open) return;
		const card = cards[currentIndex];
		const ext = (card?.dataset.ext || "").toLowerCase();
		if (ext === "mp4" || ext === "webm") return;

		if (e.ctrlKey || isZoomed) {
			e.preventDefault();
			const delta = -e.deltaY * 0.002;
			const postW = parseInt(card.dataset.width, 10) || 1000;
			const postH = parseInt(card.dataset.height, 10) || 1000;
			const r = getRenderedMediaRect(lbStage, lbMediaWrap.querySelector("img"), postW, postH);
			const baseW = r.width / (isZoomed ? currentZoomScale : 1);
			const baseH = r.height / (isZoomed ? currentZoomScale : 1);

			let newScale = (isZoomed ? currentZoomScale : 1) + delta;
			if (newScale < 1.05) {
				resetZoom();
				return;
			}
			if (newScale > 5) newScale = 5;

			currentZoomScale = newScale;
			isZoomed = true;
			lbStage.classList.add("is-zoomed");
			const targetW = Math.round(baseW * newScale);
			const targetH = Math.round(baseH * newScale);
			lbStage.style.setProperty("--zoomed-w", `${targetW}px`);
			lbStage.style.setProperty("--zoomed-h", `${targetH}px`);
			if (lbZoomToggle) {
				lbZoomToggle.textContent = `${Math.round(currentZoomScale * 100)}%`;
				lbZoomToggle.classList.add("active");
			}
		}
	}, { passive: false });

	function closeLightbox() {
		document.body.classList.remove("lightbox-open");
		resetZoom();
		if (!lightbox.open) return;
		lightbox.close();
		if (window.history.state?.lightbox) {
			window.history.back();
		}
	}

	window.addEventListener("popstate", () => {
		document.body.classList.remove("lightbox-open");
		if (lightbox.open) {
			lightbox.close();
		}
	});

	function renderLightbox(index) {
		if (index < 0 || index >= cards.length) return;
		resetZoom();
		currentIndex = index;
		const card = cards[index];
		const d = card.dataset;

		lbPostLink.href = `/posts/${d.id}`;
		lbPostLink.textContent = `#${d.id}`;
		lbScore.textContent = `★ ${d.score}`;
		if (lbRate) {
			const isPopular = window.location.pathname.startsWith("/popular") || !!document.querySelector('.nav-btn.active[href*="popular"]');
			const hot = parseInt(d.hot, 10) || 0;
			if (isPopular && hot > 0) {
				lbRate.style.display = "";
				if (lbHotVal) {
					lbHotVal.textContent = String(hot);
				}
			} else {
				lbRate.style.display = "none";
			}
		}
		lbAge.textContent = `${d.created} · ${d.width}×${d.height} ${d.ext.toUpperCase()}`;
		lbHypnohubLink.href = `https://hypnohub.net/index.php?page=post&s=view&id=${d.id}`;

		lbPrev.style.visibility = index > 0 ? "visible" : "hidden";
		lbNext.style.visibility = index < cards.length - 1 ? "visible" : "hidden";

		lbMediaWrap.innerHTML = "";
		const ext = (d.ext || "").toLowerCase();

		if (ext === "mp4" || ext === "webm") {
			lbHdToggle.hidden = true;
			const video = document.createElement("video");
			video.src = d.fileUrl;
			const cachedThumb = card.querySelector("img")?.currentSrc || d.sampleUrl || d.previewUrl;
			if (cachedThumb) video.poster = cachedThumb;
			video.controls = true;
			video.autoplay = true;
			video.loop = true;
			video.playsInline = true;
			lbMediaWrap.appendChild(video);
		} else {
			const hasDistinctSample = d.sampleUrl && d.sampleUrl !== d.fileUrl && ext !== "gif";
			lbHdToggle.hidden = !hasDistinctSample;
			lbHdToggle.classList.toggle("active", preferHD);
			lbHdToggle.textContent = preferHD ? "HD" : "Sample";

			const targetURL = preferHD || ext === "gif" ? d.fileUrl : (d.sampleUrl || d.fileUrl);
			const cachedThumb = card.querySelector("img")?.currentSrc || d.sampleUrl || d.previewUrl;

			const container = document.createElement("div");
			container.className = "lb-progressive-container";

			// Immediately display the already-loaded card thumbnail as base layer
			const previewImg = document.createElement("img");
			previewImg.className = "lb-img-preview";
			previewImg.src = cachedThumb;
			previewImg.alt = `Post #${d.id}`;
			previewImg.referrerPolicy = "no-referrer";
			container.appendChild(previewImg);

			// Loading indicator
			const loader = document.createElement("div");
			loader.className = "lb-loader";
			loader.setAttribute("aria-hidden", "true");
			container.appendChild(loader);

			// Full resolution image preloaded in background
			const fullImg = new Image();
			fullImg.className = "lb-img-full";
			fullImg.referrerPolicy = "no-referrer";
			fullImg.alt = `Post #${d.id}`;

			const onReady = () => {
				if (currentIndex !== index) return;
				loader.remove();
				fullImg.classList.add("is-loaded");
				previewImg.classList.add("is-hidden");
			};

			fullImg.src = targetURL;
			container.appendChild(fullImg);

			if (fullImg.complete) {
				onReady();
			} else {
				fullImg.decode?.().then(onReady).catch(() => {
					fullImg.onload = onReady;
				});
				fullImg.onerror = () => {
					if (currentIndex !== index) return;
					loader.remove();
				};
			}

			lbMediaWrap.appendChild(container);
		}

		// Render clickable tags
		lbTags.innerHTML = "";
		const tags = (d.tags || "").split(/\s+/).filter(Boolean);
		for (const tag of tags) {
			const a = document.createElement("a");
			a.href = `/?tags=${encodeURIComponent(tag)}`;
			a.className = "tag-chip";
			a.textContent = tag;
			lbTags.appendChild(a);
		}
	}

	cards.forEach((card, idx) => {
		const mediaLink = card.querySelector(".card-media-link");
		mediaLink?.addEventListener("click", (e) => {
			if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
			e.preventDefault();
			renderLightbox(idx);
			if (!lightbox.open) {
				document.body.classList.add("lightbox-open");
				window.history.pushState({ lightbox: true }, "");
				lightbox.showModal();
			}
		});
	});

	lbHdToggle?.addEventListener("click", () => {
		preferHD = !preferHD;
		renderLightbox(currentIndex);
	});

	lbClose?.addEventListener("click", closeLightbox);
	lbPrev?.addEventListener("click", (e) => {
		e.stopPropagation();
		renderLightbox(currentIndex - 1);
	});
	lbNext?.addEventListener("click", (e) => {
		e.stopPropagation();
		renderLightbox(currentIndex + 1);
	});

	lightbox.addEventListener("close", () => {
		document.body.classList.remove("lightbox-open");
		lbMediaWrap.innerHTML = "";
	});

	// Touch swipe navigation for mobile
	let touchStartX = 0;
	let touchStartY = 0;
	let didSwipe = false;

	lbStage?.addEventListener("touchstart", (e) => {
		if (e.touches.length === 1) {
			touchStartX = e.touches[0].clientX;
			touchStartY = e.touches[0].clientY;
			didSwipe = false;
		}
	}, { passive: true });

	lbStage?.addEventListener("touchend", (e) => {
		if (e.changedTouches.length !== 1) return;
		const dx = e.changedTouches[0].clientX - touchStartX;
		const dy = e.changedTouches[0].clientY - touchStartY;
		if (Math.abs(dx) > 45 && Math.abs(dy) < 90) {
			didSwipe = true;
			if (dx < 0 && currentIndex < cards.length - 1) {
				renderLightbox(currentIndex + 1);
			} else if (dx > 0 && currentIndex > 0) {
				renderLightbox(currentIndex - 1);
			}
		}
	}, { passive: true });

	// Click on image toggles zoom, click on black empty space closes lightbox
	lbStage?.addEventListener("click", (e) => {
		if (didSwipe) {
			didSwipe = false;
			return;
		}
		if (didDrag) {
			didDrag = false;
			return;
		}
		if (e.target.closest(".lb-nav") || e.target.closest("button") || e.target.closest("a") || e.target.closest("video")) {
			return;
		}

		const card = cards[currentIndex];
		const ext = (card?.dataset.ext || "").toLowerCase();
		const isVideo = ext === "mp4" || ext === "webm";

		if (isVideo) {
			closeLightbox();
			return;
		}

		if (isZoomed) {
			toggleZoom();
			return;
		}

		const r = getRenderedMediaRect(lbStage, lbMediaWrap.querySelector("img"), parseInt(card?.dataset.width, 10), parseInt(card?.dataset.height, 10));
		const inImage = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;

		if (inImage) {
			toggleZoom(e.clientX, e.clientY);
		} else {
			closeLightbox();
		}
	});

	// Keyboard navigation
	document.addEventListener("keydown", (e) => {
		if (!lightbox.open) return;
		if (e.key === "ArrowLeft" && currentIndex > 0) {
			e.preventDefault();
			renderLightbox(currentIndex - 1);
		} else if (e.key === "ArrowRight" && currentIndex < cards.length - 1) {
			e.preventDefault();
			renderLightbox(currentIndex + 1);
		} else if (e.key === "Escape") {
			e.preventDefault();
			closeLightbox();
		}
	});
}

// Post Detail Page progressive preloading & inline HD toggle
const detailImg = document.getElementById("detail-img");
const detailHdToggle = document.getElementById("detail-hd-toggle");
if (detailImg) {
	const fullSrc = detailImg.dataset.fullSrc;
	if (fullSrc && fullSrc !== detailImg.src && !detailImg.src.endsWith(".gif")) {
		const fullPreload = new Image();
		fullPreload.referrerPolicy = "no-referrer";
		fullPreload.src = fullSrc;
		fullPreload.decode?.().then(() => {
			if (detailImg) {
				detailImg.src = fullSrc;
			}
		}).catch(() => {
			fullPreload.onload = () => {
				if (detailImg) {
					detailImg.src = fullSrc;
				}
			};
		});
	}

	if (detailHdToggle) {
		let isHD = false;
		const sampleSrc = detailImg.src;
		const hdSrc = detailImg.dataset.hdSrc || fullSrc;
		function toggleDetailHD() {
			isHD = !isHD;
			detailImg.src = isHD ? hdSrc : sampleSrc;
			detailHdToggle.textContent = isHD ? "Switch to Sample (Fast)" : "Switch to HD Original";
			detailHdToggle.classList.toggle("active", isHD);
		}
		detailHdToggle.addEventListener("click", toggleDetailHD);
		detailImg.style.cursor = "zoom-in";
		detailImg.addEventListener("click", toggleDetailHD);
	}
}

// Reusable [data-tooltip] tap/click toggle for touch & mobile devices
document.querySelectorAll("[data-tooltip]").forEach((el) => {
	el.addEventListener("click", (e) => {
		e.stopPropagation();
		const wasOpen = el.classList.contains("is-tooltip-open");
		document.querySelectorAll("[data-tooltip].is-tooltip-open").forEach((openEl) => {
			if (openEl !== el) openEl.classList.remove("is-tooltip-open");
		});
		el.classList.toggle("is-tooltip-open", !wasOpen);
	});
});

document.addEventListener("click", () => {
	document.querySelectorAll("[data-tooltip].is-tooltip-open").forEach((el) => {
		el.classList.remove("is-tooltip-open");
	});
});

document.addEventListener("keydown", (e) => {
	if (e.key === "Escape") {
		document.querySelectorAll("[data-tooltip].is-tooltip-open").forEach((el) => {
			el.classList.remove("is-tooltip-open");
		});
	}
});