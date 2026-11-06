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
	singleAutoplay: true,
	thumbFit: "cover",
	enableLightbox: true
};

function loadSettings() {
	try {
		const raw = localStorage.getItem("hypnogaze.settings") || localStorage.getItem("hypnoview.settings");
		if (!raw) return structuredClone(DEFAULT_SETTINGS);
		const parsed = JSON.parse(raw);
		const def = structuredClone(DEFAULT_SETTINGS);
		return {
			...def,
			...parsed,
			thumbFit: parsed.thumbFit === "contain" ? "contain" : "cover",
			enableLightbox: parsed.enableLightbox !== false,
			defaultWindow: { ...def.defaultWindow, ...(parsed.defaultWindow || {}) },
			defaultSort: { ...def.defaultSort, ...(parsed.defaultSort || {}) },
			blockedTags: Array.isArray(parsed.blockedTags) ? parsed.blockedTags : []
		};
	} catch (_) {
		return structuredClone(DEFAULT_SETTINGS);
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
	el.dataset.thumbFit = s.thumbFit === "contain" ? "contain" : "cover";

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

		const tagSpan = document.createElement("span");
		tagSpan.className = "blocklist-chip-tag";
		tagSpan.textContent = tag;

		const removeBtn = document.createElement("button");
		removeBtn.type = "button";
		removeBtn.className = "blocklist-chip-remove";
		removeBtn.setAttribute("aria-label", `Remove blocked tag ${tag}`);

		const iconSpan = document.createElement("span");
		iconSpan.className = "material-symbols-rounded";
		iconSpan.setAttribute("aria-hidden", "true");
		iconSpan.textContent = "close";

		removeBtn.appendChild(iconSpan);
		chip.appendChild(tagSpan);
		chip.appendChild(removeBtn);

		removeBtn.addEventListener("click", () => {
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
	setSegmentedActive("setting-thumb-fit", currentSettings.thumbFit || "cover");
	setSegmentedActive("setting-enable-lightbox", currentSettings.enableLightbox !== false ? "true" : "false");

	const maxColsInput = document.getElementById("setting-max-cols");
	const maxColsVal = document.getElementById("setting-max-cols-val");
	if (maxColsInput) maxColsInput.value = currentSettings.maxColumns;
	if (maxColsVal) maxColsVal.textContent = `${currentSettings.maxColumns} cols`;

	const minWidthInput = document.getElementById("setting-min-width");
	const minWidthVal = document.getElementById("setting-min-width-val");
	if (minWidthInput) minWidthInput.value = currentSettings.minColumnWidth;
	if (minWidthVal) minWidthVal.textContent = `${currentSettings.minColumnWidth}px`;

	setSegmentedActive("setting-sort-browse", currentSettings.defaultSort?.browse || "new");
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

document.querySelectorAll("#setting-thumb-fit .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.thumbFit = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-thumb-fit", currentSettings.thumbFit);
		updateSettingsJsonTextarea();
	});
});

document.querySelectorAll("#setting-enable-lightbox .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		currentSettings.enableLightbox = btn.dataset.value === "true";
		saveSettings(currentSettings);
		setSegmentedActive("setting-enable-lightbox", currentSettings.enableLightbox ? "true" : "false");
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

document.querySelectorAll("#setting-sort-browse .segmented-btn").forEach((btn) => {
	btn.addEventListener("click", () => {
		if (!currentSettings.defaultSort) currentSettings.defaultSort = {};
		currentSettings.defaultSort.browse = btn.dataset.value;
		saveSettings(currentSettings);
		setSegmentedActive("setting-sort-browse", currentSettings.defaultSort.browse);
		updateSettingsJsonTextarea();
	});
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
			thumbFit: parsed.thumbFit === "contain" ? "contain" : "cover",
			enableLightbox: parsed.enableLightbox !== false,
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
		saveSettings(structuredClone(DEFAULT_SETTINGS));
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

// Unified Zoomable Media Viewer & Lightbox Implementation
function getRenderedMediaRect(stageEl, mediaEl, postW, postH) {
	const stageRect = stageEl.getBoundingClientRect();
	const sw = stageEl.clientWidth || stageRect.width;
	const sh = stageEl.clientHeight || stageRect.height;
	const ar = (postW && postH) ? (postW / postH) : ((mediaEl?.naturalWidth && mediaEl?.naturalHeight) ? (mediaEl.naturalWidth / mediaEl.naturalHeight) : 1);
	let rw, rh, left, top;
	if (sw / (sh || 1) > ar) {
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

function createZoomableMediaViewer(options) {
	const {
		stage,
		mediaWrap,
		zoomToggleBtn,
		hdToggleBtn,
		onOutsideClick,
		isMediaActive = () => true
	} = options;

	let currentPost = null;
	let currentThumbSrc = "";
	let isZoomed = false;
	let currentZoomScale = 1;
	let preferHD = currentSettings.preferHD ?? true;

	let isDragging = false;
	let startX = 0, startY = 0;
	let scrollStartX = 0, scrollStartY = 0;
	let didDrag = false;
	let targetW = 0, targetH = 0;

	function resetZoom() {
		isZoomed = false;
		currentZoomScale = 1;
		targetW = 0;
		targetH = 0;
		stage.classList.remove("is-zoomed", "is-dragging");
		stage.style.removeProperty("--zoomed-w");
		stage.style.removeProperty("--zoomed-h");
		if (zoomToggleBtn) {
			zoomToggleBtn.textContent = "Fit";
			zoomToggleBtn.classList.remove("active");
		}
	}

	function toggleZoom(clickX, clickY) {
		if (isZoomed) {
			resetZoom();
			return;
		}
		if (!currentPost) return;
		const ext = (currentPost.ext || "").toLowerCase();
		if (ext === "mp4" || ext === "webm") return;

		const postW = parseInt(currentPost.width, 10) || 1000;
		const postH = parseInt(currentPost.height, 10) || 1000;
		const activeImg = mediaWrap.querySelector(".img-full.is-loaded") || mediaWrap.querySelector(".img-preview") || mediaWrap.querySelector("img");
		const r = getRenderedMediaRect(stage, activeImg, postW, postH);
		const sw = stage.clientWidth;
		const sh = stage.clientHeight;
		const ar = postW / postH;

		const zoomMode = currentSettings.zoomMode || "fill-width";
		const maxWidthCap = Math.min(sw, 1000);

		let computedW;
		if (zoomMode === "original") {
			computedW = Math.min(postW, maxWidthCap);
		} else if (zoomMode === "2x") {
			computedW = Math.min(Math.round(r.width * 2), maxWidthCap);
		} else if (zoomMode === "3x") {
			computedW = Math.min(Math.round(r.width * 3), maxWidthCap);
		} else {
			// "fill-width" default: fills stage width (or at least capped container on detail)
			computedW = maxWidthCap;
		}

		if (computedW <= r.width + 1) return; // already at or above target width; don't enter a no-op zoom state

		targetW = Math.round(computedW);
		targetH = Math.round(targetW / ar);

		currentZoomScale = r.width > 0 ? (targetW / r.width) : 1;
		isZoomed = true;
		stage.classList.add("is-zoomed");
		stage.style.setProperty("--zoomed-w", `${targetW}px`);
		stage.style.setProperty("--zoomed-h", `${targetH}px`);

		if (zoomToggleBtn) {
			zoomToggleBtn.textContent = `${Math.round(currentZoomScale * 100)}%`;
			zoomToggleBtn.classList.add("active");
		}

		if (clickX !== undefined && clickY !== undefined) {
			const u = Math.max(0, Math.min(1, (clickX - r.left) / (r.width || 1)));
			const v = Math.max(0, Math.min(1, (clickY - r.top) / (r.height || 1)));
			requestAnimationFrame(() => {
				stage.scrollLeft = (u * targetW) - (clickX - stage.getBoundingClientRect().left);
				stage.scrollTop = (v * targetH) - (clickY - stage.getBoundingClientRect().top);
			});
		} else {
			requestAnimationFrame(() => {
				stage.scrollLeft = (targetW - sw) / 2;
				stage.scrollTop = (targetH - sh) / 2;
			});
		}
	}

	function onMouseDown(e) {
		if (!isZoomed || e.button !== 0) return;
		if (e.target.closest(".lb-nav") || e.target.closest("button") || e.target.closest("a")) return;
		isDragging = true;
		didDrag = false;
		startX = e.clientX;
		startY = e.clientY;
		scrollStartX = stage.scrollLeft;
		scrollStartY = stage.scrollTop;
	}

	function onWindowMouseMove(e) {
		if (!isDragging) return;
		const dx = e.clientX - startX;
		const dy = e.clientY - startY;
		if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
			didDrag = true;
			stage.classList.add("is-dragging");
		}
		stage.scrollLeft = scrollStartX - dx;
		stage.scrollTop = scrollStartY - dy;
	}

	function onWindowMouseUp() {
		if (isDragging) {
			isDragging = false;
			stage.classList.remove("is-dragging");
		}
	}

	function onMouseMove(e) {
		if (isZoomed) {
			if (!isDragging) {
				const rect = mediaWrap.getBoundingClientRect();
				const inMedia = e.clientX >= rect.left && e.clientX <= rect.right && e.clientY >= rect.top && e.clientY <= rect.bottom;
				stage.style.cursor = inMedia ? "zoom-out" : (onOutsideClick ? "pointer" : "zoom-out");
			}
			return;
		}
		if (!currentPost) return;
		const ext = (currentPost.ext || "").toLowerCase();
		if (ext === "mp4" || ext === "webm") {
			stage.style.cursor = "default";
			return;
		}
		const activeImg = mediaWrap.querySelector(".img-full.is-loaded") || mediaWrap.querySelector(".img-preview") || mediaWrap.querySelector("img");
		const r = getRenderedMediaRect(stage, activeImg, parseInt(currentPost.width, 10), parseInt(currentPost.height, 10));
		const inImage = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
		stage.style.cursor = inImage ? "zoom-in" : (onOutsideClick ? "pointer" : "default");
	}

	function onWheel(e) {
		if (!isMediaActive()) return;
		if (!currentPost) return;
		const ext = (currentPost.ext || "").toLowerCase();
		if (ext === "mp4" || ext === "webm") return;

		if (e.ctrlKey || isZoomed) {
			e.preventDefault();
			const delta = -e.deltaY * 0.002;
			const postW = parseInt(currentPost.width, 10) || 1000;
			const postH = parseInt(currentPost.height, 10) || 1000;
			const activeImg = mediaWrap.querySelector(".img-full.is-loaded") || mediaWrap.querySelector(".img-preview") || mediaWrap.querySelector("img");
			const r = getRenderedMediaRect(stage, activeImg, postW, postH);
			const sw = stage.clientWidth;
			const ar = postW / postH;
			const maxWidthCap = Math.min(sw, 1000);

			const baseW = isZoomed && targetW > 0 ? (targetW / currentZoomScale) : r.width;
			let newScale = (isZoomed ? currentZoomScale : 1) + delta;
			if (newScale < 1.05) {
				resetZoom();
				return;
			}
			if (newScale > 5) newScale = 5;

			let computedW = Math.round(baseW * newScale);
			if (computedW > maxWidthCap) {
				computedW = maxWidthCap;
			}
			const computedH = Math.round(computedW / ar);

			targetW = computedW;
			targetH = computedH;
			currentZoomScale = newScale;
			isZoomed = true;
			stage.classList.add("is-zoomed");
			stage.style.setProperty("--zoomed-w", `${targetW}px`);
			stage.style.setProperty("--zoomed-h", `${targetH}px`);
			if (zoomToggleBtn) {
				zoomToggleBtn.textContent = `${Math.round(currentZoomScale * 100)}%`;
				zoomToggleBtn.classList.add("active");
			}
		}
	}

	function onClick(e) {
		if (didDrag) {
			didDrag = false;
			return;
		}
		if (e.target.closest(".lb-nav") || e.target.closest("button") || e.target.closest("a") || e.target.closest("video")) {
			return;
		}

		if (!currentPost) return;
		const ext = (currentPost.ext || "").toLowerCase();
		const isVideo = ext === "mp4" || ext === "webm";

		if (isVideo) {
			if (onOutsideClick) onOutsideClick();
			return;
		}

		let inMedia = false;
		if (isZoomed) {
			const mr = mediaWrap.getBoundingClientRect();
			inMedia = e.clientX >= mr.left && e.clientX <= mr.right && e.clientY >= mr.top && e.clientY <= mr.bottom;
		} else {
			const activeImg = mediaWrap.querySelector(".img-full.is-loaded") || mediaWrap.querySelector(".img-preview") || mediaWrap.querySelector("img");
			const r = getRenderedMediaRect(stage, activeImg, parseInt(currentPost.width, 10), parseInt(currentPost.height, 10));
			inMedia = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
		}

		// Clicking outside the rendered media in lightbox closes the lightbox even when zoomed
		if (!inMedia && onOutsideClick) {
			onOutsideClick();
			return;
		}

		if (isZoomed) {
			toggleZoom();
			return;
		}

		if (inMedia) {
			toggleZoom(e.clientX, e.clientY);
		}
	}

	function loadMedia(postData, thumbSrc) {
		currentPost = postData;
		currentThumbSrc = thumbSrc;
		resetZoom();

		const ext = (postData.ext || "").toLowerCase();
		mediaWrap.innerHTML = "";

		if (ext === "mp4" || ext === "webm") {
			if (zoomToggleBtn) zoomToggleBtn.style.display = "none";
			if (hdToggleBtn) hdToggleBtn.style.display = "none";

			const video = document.createElement("video");
			video.src = postData.fileUrl;
			const cachedThumb = thumbSrc || postData.sampleUrl || postData.previewUrl;
			if (cachedThumb) video.poster = cachedThumb;
			video.controls = true;
			video.autoplay = true;
			video.loop = true;
			video.playsInline = true;
			video.className = "detail-media";
			mediaWrap.appendChild(video);
			return;
		}

		if (zoomToggleBtn) {
			zoomToggleBtn.style.display = "";
			zoomToggleBtn.textContent = "Fit";
			zoomToggleBtn.classList.remove("active");
		}

		const hasDistinctSample = postData.sampleUrl && postData.sampleUrl !== postData.fileUrl && ext !== "gif";
		if (hdToggleBtn) {
			hdToggleBtn.style.display = hasDistinctSample ? "" : "none";
			hdToggleBtn.textContent = preferHD ? "HD" : "Sample";
			hdToggleBtn.classList.toggle("active", preferHD);
			hdToggleBtn.title = preferHD ? "Switch to Sample resolution" : "Switch to HD original resolution";
		}

		const targetURL = preferHD || ext === "gif" ? postData.fileUrl : (postData.sampleUrl || postData.fileUrl);
		const cachedThumb = thumbSrc || postData.sampleUrl || postData.previewUrl;

		const container = document.createElement("div");
		container.className = "progressive-media-container lb-progressive-container";
		if (postData.width && postData.height) {
			container.style.setProperty("--media-ar", `${postData.width} / ${postData.height}`);
		}

		// 1. Base layer: cached thumbnail (instant)
		const previewImg = document.createElement("img");
		previewImg.className = "img-preview lb-img-preview";
		previewImg.src = cachedThumb;
		previewImg.alt = `Post #${postData.id}`;
		previewImg.referrerPolicy = "no-referrer";
		container.appendChild(previewImg);

		// 2. Loading spinner
		const loader = document.createElement("div");
		loader.className = "media-loader lb-loader";
		loader.setAttribute("aria-hidden", "true");
		container.appendChild(loader);

		// 3. High-res target image preloaded in background
		const fullImg = new Image();
		fullImg.className = "img-full lb-img-full";
		fullImg.referrerPolicy = "no-referrer";
		fullImg.alt = `Post #${postData.id}`;

		const onReady = () => {
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
				loader.remove();
			};
		}

		mediaWrap.appendChild(container);
	}

	stage.addEventListener("mousedown", onMouseDown);
	window.addEventListener("mousemove", onWindowMouseMove);
	window.addEventListener("mouseup", onWindowMouseUp);
	stage.addEventListener("mousemove", onMouseMove);
	stage.addEventListener("wheel", onWheel, { passive: false });
	stage.addEventListener("click", onClick);

	function onZoomToggleClick(e) {
		e.stopPropagation();
		toggleZoom();
	}

	function onHdToggleClick(e) {
		e.stopPropagation();
		preferHD = !preferHD;
		currentSettings.preferHD = preferHD;
		saveSettings(currentSettings);
		if (currentPost) {
			loadMedia(currentPost, currentThumbSrc);
		}
	}

	zoomToggleBtn?.addEventListener("click", onZoomToggleClick);
	hdToggleBtn?.addEventListener("click", onHdToggleClick);

	return {
		setMedia(postData, thumbSrc) {
			loadMedia(postData, thumbSrc);
		},
		resetZoom,
		toggleZoom,
		destroy() {
			resetZoom();
			stage.removeEventListener("mousedown", onMouseDown);
			window.removeEventListener("mousemove", onWindowMouseMove);
			window.removeEventListener("mouseup", onWindowMouseUp);
			stage.removeEventListener("mousemove", onMouseMove);
			stage.removeEventListener("wheel", onWheel);
			stage.removeEventListener("click", onClick);
			zoomToggleBtn?.removeEventListener("click", onZoomToggleClick);
			hdToggleBtn?.removeEventListener("click", onHdToggleClick);
		},
		isZoomed: () => isZoomed
	};
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
	const lbGroupBadge = document.getElementById("lb-group-badge");
	const lbGroupBadgeVal = document.getElementById("lb-group-badge-val");
	const lbGroupNav = document.getElementById("lb-group-nav");
	const groupPostsCache = new Map();

	let currentCardIndex = 0;
	let currentGroupPosts = [];
	let currentGroupIndex = 0;

	function closeLightbox() {
		document.body.classList.remove("lightbox-open");
		lbViewer.resetZoom();
		if (!lightbox.open) return;
		lightbox.close();
		if (window.history.state?.lightbox) {
			window.history.back();
		}
	}

	const lbViewer = createZoomableMediaViewer({
		stage: lbStage,
		mediaWrap: lbMediaWrap,
		zoomToggleBtn: lbZoomToggle,
		hdToggleBtn: lbHdToggle,
		onOutsideClick: closeLightbox,
		isMediaActive: () => lightbox.open
	});

	window.addEventListener("popstate", () => {
		document.body.classList.remove("lightbox-open");
		if (lightbox.open) {
			lightbox.close();
		}
	});

	function updateGroupNavArrows() {
		if (!currentGroupPosts || currentGroupPosts.length <= 1) {
			if (lbPrev) lbPrev.style.display = "none";
			if (lbNext) lbNext.style.display = "none";
			return;
		}
		if (lbPrev) lbPrev.style.display = currentGroupIndex > 0 ? "flex" : "none";
		if (lbNext) lbNext.style.display = currentGroupIndex < currentGroupPosts.length - 1 ? "flex" : "none";
	}

	function goToGroupPage(idx) {
		if (!currentGroupPosts || idx < 0 || idx >= currentGroupPosts.length) return;
		currentGroupIndex = idx;
		const gp = currentGroupPosts[idx];

		if (lbGroupNav) {
			lbGroupNav.querySelectorAll(".lb-page-pill").forEach((p, i) => {
				p.classList.toggle("active", i === idx);
			});
		}

		renderLightboxPostData({
			id: gp.id,
			score: gp.score,
			hot: 0,
			created: "",
			width: gp.width,
			height: gp.height,
			ext: gp.file_ext,
			fileUrl: gp.file_url,
			sampleUrl: gp.sample_url,
			previewUrl: gp.preview_url,
			tags: gp.tags
		}, gp.sample_url || gp.preview_url);

		updateGroupNavArrows();
	}

	function renderLightboxPostData(d, thumbSrc) {
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
		const metaAge = d.created ? `${d.created} · ` : "";
		lbAge.textContent = `${metaAge}${d.width}×${d.height} ${(d.ext || "").toUpperCase()}`;
		lbHypnohubLink.href = `https://hypnohub.net/index.php?page=post&s=view&id=${d.id}`;

		lbViewer.setMedia(d, thumbSrc);

		// Render clickable tags
		lbTags.innerHTML = "";
		const tags = Array.isArray(d.tags) ? d.tags : (d.tags || "").split(/\s+/).filter(Boolean);
		for (const tag of tags) {
			const a = document.createElement("a");
			a.href = `/?tags=${encodeURIComponent(tag)}`;
			a.className = "tag-chip";
			a.textContent = tag;
			lbTags.appendChild(a);
		}
	}

	function renderLightbox(cardIndex) {
		if (cardIndex < 0 || cardIndex >= cards.length) return;
		currentCardIndex = cardIndex;
		const card = cards[cardIndex];
		const d = card.dataset;
		const thumbSrc = card.querySelector("img")?.currentSrc || d.sampleUrl || d.previewUrl;

		currentGroupPosts = [];
		currentGroupIndex = 0;
		updateGroupNavArrows();

		renderLightboxPostData(d, thumbSrc);

		const groupCount = parseInt(d.groupCount, 10) || 1;
		if (lbGroupBadge) {
			if (groupCount > 1) {
				lbGroupBadge.style.display = "inline-flex";
				if (lbGroupBadgeVal) lbGroupBadgeVal.textContent = String(groupCount);
			} else {
				lbGroupBadge.style.display = "none";
			}
		}

		if (lbGroupNav) {
			if (groupCount <= 1) {
				lbGroupNav.style.display = "none";
				lbGroupNav.innerHTML = "";
				currentGroupPosts = [];
				currentGroupIndex = 0;
				updateGroupNavArrows();
			} else {
				lbGroupNav.style.display = "flex";
				const renderPills = (groupPosts, activeId) => {
					currentGroupPosts = groupPosts;
					const foundIdx = groupPosts.findIndex(gp => gp.id === activeId);
					currentGroupIndex = foundIdx >= 0 ? foundIdx : 0;
					updateGroupNavArrows();

					lbGroupNav.innerHTML = "";
					groupPosts.forEach((gp, gIdx) => {
						const pill = document.createElement("button");
						pill.type = "button";
						pill.className = "lb-page-pill" + (gIdx === currentGroupIndex ? " active" : "");
						pill.textContent = String(gIdx + 1);
						pill.title = `Page ${gIdx + 1} (Post #${gp.id})`;
						pill.addEventListener("click", (e) => {
							e.stopPropagation();
							goToGroupPage(gIdx);
						});
						lbGroupNav.appendChild(pill);
					});
				};

				if (groupPostsCache.has(d.id)) {
					renderPills(groupPostsCache.get(d.id), parseInt(d.id, 10));
				} else {
					fetch(`/api/v1/posts/${d.id}`)
						.then(r => r.json())
						.then(data => {
							if (currentCardIndex !== cardIndex) return;
							if (data?.group_posts && data.group_posts.length > 1) {
								groupPostsCache.set(d.id, data.group_posts);
								renderPills(data.group_posts, parseInt(d.id, 10));
							}
						})
						.catch(() => {});
				}
			}
		}
	}

	cards.forEach((card, idx) => {
		const mediaLink = card.querySelector(".card-media-link");
		mediaLink?.addEventListener("click", (e) => {
			if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
			if (!currentSettings.enableLightbox) return;
			e.preventDefault();
			renderLightbox(idx);
			if (!lightbox.open) {
				document.body.classList.add("lightbox-open");
				window.history.pushState({ lightbox: true }, "");
				lightbox.showModal();
			}
		});
	});

	lbClose?.addEventListener("click", closeLightbox);
	lbPrev?.addEventListener("click", (e) => {
		e.stopPropagation();
		if (currentGroupPosts.length > 1 && currentGroupIndex > 0) {
			goToGroupPage(currentGroupIndex - 1);
		}
	});
	lbNext?.addEventListener("click", (e) => {
		e.stopPropagation();
		if (currentGroupPosts.length > 1 && currentGroupIndex < currentGroupPosts.length - 1) {
			goToGroupPage(currentGroupIndex + 1);
		}
	});

	lightbox.addEventListener("close", () => {
		document.body.classList.remove("lightbox-open");
		lbMediaWrap.innerHTML = "";
	});

	// Touch swipe navigation for mobile (across pages of a group)
	let touchStartX = 0;
	let touchStartY = 0;

	lbStage?.addEventListener("touchstart", (e) => {
		if (e.touches.length === 1) {
			touchStartX = e.touches[0].clientX;
			touchStartY = e.touches[0].clientY;
		}
	}, { passive: true });

	lbStage?.addEventListener("touchend", (e) => {
		if (lbViewer.isZoomed()) return;
		if (e.changedTouches.length !== 1) return;
		const dx = e.changedTouches[0].clientX - touchStartX;
		const dy = e.changedTouches[0].clientY - touchStartY;
		if (Math.abs(dx) > 45 && Math.abs(dy) < 90) {
			if (currentGroupPosts.length > 1) {
				if (dx < 0 && currentGroupIndex < currentGroupPosts.length - 1) {
					goToGroupPage(currentGroupIndex + 1);
				} else if (dx > 0 && currentGroupIndex > 0) {
					goToGroupPage(currentGroupIndex - 1);
				}
			}
		}
	}, { passive: true });

	// Keyboard navigation: ArrowLeft/ArrowRight navigates group pages only
	document.addEventListener("keydown", (e) => {
		if (!lightbox.open) return;
		if (e.key === "ArrowLeft") {
			if (currentGroupPosts.length > 1 && currentGroupIndex > 0) {
				e.preventDefault();
				goToGroupPage(currentGroupIndex - 1);
			}
		} else if (e.key === "ArrowRight") {
			if (currentGroupPosts.length > 1 && currentGroupIndex < currentGroupPosts.length - 1) {
				e.preventDefault();
				goToGroupPage(currentGroupIndex + 1);
			}
		} else if (e.key === "Escape") {
			e.preventDefault();
			closeLightbox();
		}
	});
}

// Post Detail Page progressive preloading & unified zoom viewer
let currentDetailViewer = null;

function initPostDetail() {
	const postStage = document.getElementById("post-stage");
	if (!postStage) return;

	if (currentDetailViewer) {
		currentDetailViewer.destroy();
		currentDetailViewer = null;
	}

	const mediaWrap = postStage.querySelector(".media-wrap");
	if (!mediaWrap) return;

	const zoomToggleBtn = document.getElementById("detail-zoom-toggle");
	const hdToggleBtn = document.getElementById("detail-hd-toggle");
	const d = postStage.dataset;

	currentDetailViewer = createZoomableMediaViewer({
		stage: postStage,
		mediaWrap: mediaWrap,
		zoomToggleBtn: zoomToggleBtn,
		hdToggleBtn: hdToggleBtn,
		onOutsideClick: null,
		isMediaActive: () => {
			const lb = document.getElementById("lightbox");
			return !lb || !lb.open;
		}
	});

	currentDetailViewer.setMedia({
		id: d.id,
		ext: d.ext,
		width: d.width,
		height: d.height,
		fileUrl: d.fileUrl,
		sampleUrl: d.sampleUrl,
		previewUrl: d.previewUrl
	});
}

initPostDetail();

document.body.addEventListener("htmx:afterSwap", (e) => {
	if (e.detail?.target?.id === "post-detail" || document.getElementById("post-detail")) {
		initPostDetail();
	}
});

// Arrow key navigation on post detail page
window.addEventListener("keydown", (e) => {
	if (document.activeElement && (document.activeElement.tagName === "INPUT" || document.activeElement.tagName === "TEXTAREA")) return;
	const lb = document.getElementById("lightbox");
	if (lb && lb.open) return; // handled by lightbox

	if (e.key === "ArrowLeft") {
		const prevBtn = document.getElementById("group-prev-btn");
		if (prevBtn) {
			e.preventDefault();
			prevBtn.click();
		}
	} else if (e.key === "ArrowRight") {
		const nextBtn = document.getElementById("group-next-btn");
		if (nextBtn) {
			e.preventDefault();
			nextBtn.click();
		}
	}
});

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