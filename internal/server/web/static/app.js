// Progressive enhancement for Hypnogaze mobile-friendly feed:
// 1. Tag search autocomplete with keyboard navigation & AbortController
// 2. Tag chip toggle (add/remove) & active tag badges
// 3. Interactive media lightbox with 0ms progressive thumbnail preview and background scroll locking
// 4. Post detail progressive preloading & inline HD toggle
// 5. Progressive Web App (PWA) Service Worker registration & self-update checks

document.documentElement.classList.add("js");

// Topbar scroll-reveal & dynamic height tracking
function updateTopbarHeight() {
	const topbar = document.querySelector(".topbar");
	if (!topbar) return;
	const h = topbar.offsetHeight || 55;
	document.documentElement.style.setProperty("--topbar-height", `${h}px`);
}

let lastScrollY = window.scrollY;

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
	if (maxCols >= 1 && maxCols <= 10) {
		el.style.setProperty("--grid-max-cols", String(maxCols));
	}

	const minW = parseInt(s.minColumnWidth, 10);
	if (minW >= 150 && minW <= 420) {
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

function initSettingsUI() {
	const viewModeToggle = document.getElementById("view-mode-toggle");
	if (viewModeToggle) {
		viewModeToggle.onclick = () => {
			currentSettings.viewMode = currentSettings.viewMode === "single" ? "grid" : "single";
			saveSettings(currentSettings);
			syncSettingsDialogUI();
		};
	}

	const settingsDialog = document.getElementById("settings-dialog");
	const settingsToggle = document.getElementById("settings-toggle");
	const settingsCloseBtn = document.getElementById("settings-close-btn");
	const settingsDoneBtn = document.getElementById("settings-done-btn");
	const settingsResetBtn = document.getElementById("settings-reset-btn");
	const blockedTagsBtn = document.getElementById("blocked-tags-btn");

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
		settingsDialog.onclose = () => {
			const currentSnapshot = getServerPrefsSnapshot();
			if (serverSnapshot !== null && serverSnapshot !== currentSnapshot) {
				window.location.reload();
			}
		};

		settingsDialog.onclick = (e) => {
			if (e.target === settingsDialog) {
				closeSettingsDialog();
			}
		};
	}

	if (settingsToggle) settingsToggle.onclick = () => openSettingsDialog();
	if (settingsCloseBtn) settingsCloseBtn.onclick = closeSettingsDialog;
	if (settingsDoneBtn) settingsDoneBtn.onclick = closeSettingsDialog;
	if (blockedTagsBtn) blockedTagsBtn.onclick = () => openSettingsDialog(document.getElementById("blocklist-input"));

	// Dialog Interactive Controls
	document.querySelectorAll("#setting-theme .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.theme = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-theme", currentSettings.theme);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-view-mode .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.viewMode = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-view-mode", currentSettings.viewMode);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-thumb-fit .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.thumbFit = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-thumb-fit", currentSettings.thumbFit);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-enable-lightbox .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.enableLightbox = btn.dataset.value === "true";
			saveSettings(currentSettings);
			setSegmentedActive("setting-enable-lightbox", currentSettings.enableLightbox ? "true" : "false");
			updateSettingsJsonTextarea();
		};
	});

	const settingMaxCols = document.getElementById("setting-max-cols");
	if (settingMaxCols) {
		settingMaxCols.oninput = () => {
			const val = parseInt(settingMaxCols.value, 10);
			currentSettings.maxColumns = val;
			const valBadge = document.getElementById("setting-max-cols-val");
			if (valBadge) valBadge.textContent = `${val} cols`;
			saveSettings(currentSettings);
			updateSettingsJsonTextarea();
		};
	}

	const settingMinWidth = document.getElementById("setting-min-width");
	if (settingMinWidth) {
		settingMinWidth.oninput = () => {
			const val = parseInt(settingMinWidth.value, 10);
			currentSettings.minColumnWidth = val;
			const valBadge = document.getElementById("setting-min-width-val");
			if (valBadge) valBadge.textContent = `${val}px`;
			saveSettings(currentSettings);
			updateSettingsJsonTextarea();
		};
	}

	document.querySelectorAll("#setting-sort-browse .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			if (!currentSettings.defaultSort) currentSettings.defaultSort = {};
			currentSettings.defaultSort.browse = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-sort-browse", currentSettings.defaultSort.browse);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-window-popular .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.defaultWindow.popular = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-window-popular", currentSettings.defaultWindow.popular);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-window-browse .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.defaultWindow.browse = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-window-browse", currentSettings.defaultWindow.browse);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-default-rating .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.defaultRating = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-default-rating", currentSettings.defaultRating);
			updateSettingsJsonTextarea();
		};
	});

	document.querySelectorAll("#setting-zoom-mode .segmented-btn").forEach((btn) => {
		btn.onclick = () => {
			currentSettings.zoomMode = btn.dataset.value;
			saveSettings(currentSettings);
			setSegmentedActive("setting-zoom-mode", currentSettings.zoomMode);
			updateSettingsJsonTextarea();
		};
	});

	const preferHdInput = document.getElementById("setting-prefer-hd");
	if (preferHdInput) {
		preferHdInput.onchange = (e) => {
			currentSettings.preferHD = e.target.checked;
			saveSettings(currentSettings);
			updateSettingsJsonTextarea();
		};
	}

	const singleAutoplayInput = document.getElementById("setting-single-autoplay");
	if (singleAutoplayInput) {
		singleAutoplayInput.onchange = (e) => {
			currentSettings.singleAutoplay = e.target.checked;
			saveSettings(currentSettings);
			updateSettingsJsonTextarea();
		};
	}

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

	if (blocklistAddBtn) {
		blocklistAddBtn.onclick = () => {
			if (blocklistInput) addBlocklistTag(blocklistInput.value);
		};
	}

	bindAutocompleteBox(blocklistInput, blocklistSuggestions, (item) => {
		const tag = item.dataset.addBlockTag;
		if (tag) {
			addBlocklistTag(tag);
		}
		blocklistInput?.focus();
	});

	const jsonApplyBtn = document.getElementById("settings-json-apply-btn");
	if (jsonApplyBtn) {
		jsonApplyBtn.onclick = () => {
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
		};
	}

	if (settingsResetBtn) {
		settingsResetBtn.onclick = () => {
			if (confirm("Reset all settings & preferences to defaults?")) {
				saveSettings(structuredClone(DEFAULT_SETTINGS));
				syncSettingsDialogUI();
			}
		};
	}

	syncSettingsDialogUI();
}

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
}

function initSearchUI() {
	const searchInput = document.getElementById("tag-search-input");
	const suggestionsBox = document.getElementById("tag-suggestions");

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

		btn.onclick = (e) => {
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
		};
	});

	// Clickable active tag badges in results summary remove tag on click
	document.querySelectorAll(".active-tag[data-remove-tag]").forEach((btn) => {
		btn.onclick = (e) => {
			if (!searchInput) return;
			e.preventDefault();
			const removeTag = btn.dataset.removeTag;
			let tokens = getSearchTokens().filter((t) => t !== removeTag && t !== "-" + removeTag);
			searchInput.value = tokens.join(" ") + (tokens.length > 0 ? " " : "");
			searchInput.form?.submit();
		};
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

		window.__hypnogazeSyncDrawer = syncDrawerState;

		mobileFilterToggle.onclick = (e) => {
			e.preventDefault();
			const nextState = !mobileFilterCheckbox?.checked;
			syncDrawerState(nextState);
		};

		if (mobileFilterClose) {
			mobileFilterClose.onclick = (e) => {
				e.preventDefault();
				syncDrawerState(false);
			};
		}

		// Keyboard accessibility for label buttons (Enter / Space)
		mobileFilterToggle.onkeydown = (e) => {
			if (e.key === " " || e.key === "Enter") {
				e.preventDefault();
				const nextState = !mobileFilterCheckbox?.checked;
				syncDrawerState(nextState);
			}
		};

		if (mobileFilterClose) {
			mobileFilterClose.onkeydown = (e) => {
				if (e.key === " " || e.key === "Enter") {
					e.preventDefault();
					syncDrawerState(false);
				}
			};
		}

		// Close on backdrop click (clicking outside .controls-panel)
		indexSidebar.onclick = (e) => {
			if (e.target === indexSidebar || e.target.classList.contains("mobile-sidebar-backdrop")) {
				syncDrawerState(false);
			}
		};
	} else {
		window.__hypnogazeSyncDrawer = null;
	}
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
	let scale = 1;
	let translateX = 0;
	let translateY = 0;
	let isDragging = false;
	let startPointerX = 0;
	let startPointerY = 0;
	let startTransX = 0;
	let startTransY = 0;
	let didDrag = false;
	let activePointers = new Map();
	let pinchStartDist = 0;
	let pinchStartScale = 1;
	let pinchCenterStageX = 0;
	let pinchCenterStageY = 0;

	function isVideo() {
		return currentPost && (currentPost.ext === "mp4" || currentPost.ext === "webm");
	}

	function getMediaElement() {
		return mediaWrap.querySelector(".img-full.is-loaded, .img-full, .img-preview, .detail-media, video, img");
	}

	function getNaturalDimensions() {
		const mediaEl = getMediaElement();
		const nw = currentPost?.width || mediaEl?.naturalWidth || mediaEl?.videoWidth || 0;
		const nh = currentPost?.height || mediaEl?.naturalHeight || mediaEl?.videoHeight || 0;
		return { nw, nh };
	}

	function updateTransform() {
		const mediaEl = getMediaElement();
		if (!mediaEl) return;
		if (isZoomed) {
			stage.classList.add("is-zoomed");
			mediaWrap.style.transform = `translate3d(${translateX}px, ${translateY}px, 0) scale(${scale})`;
			if (zoomToggleBtn) {
				zoomToggleBtn.classList.add("active");
				zoomToggleBtn.setAttribute("aria-pressed", "true");
				const icon = zoomToggleBtn.querySelector(".material-symbols-rounded");
				if (icon) {
					icon.textContent = "zoom_out";
				} else {
					zoomToggleBtn.textContent = "Reset";
				}
			}
		} else {
			stage.classList.remove("is-zoomed");
			mediaWrap.style.transform = "none";
			if (zoomToggleBtn) {
				zoomToggleBtn.classList.remove("active");
				zoomToggleBtn.setAttribute("aria-pressed", "false");
				const icon = zoomToggleBtn.querySelector(".material-symbols-rounded");
				if (icon) {
					icon.textContent = "zoom_in";
				} else {
					zoomToggleBtn.textContent = "Fit";
				}
			}
		}
	}

	function clampTranslation() {
		const mediaEl = getMediaElement();
		if (!mediaEl) return;
		const stageRect = stage.getBoundingClientRect();
		const baseRect = getRenderedMediaRect(stage, mediaEl, currentPost?.width, currentPost?.height);
		const curW = baseRect.width * scale;
		const curH = baseRect.height * scale;

		let maxOffsetX = Math.max(0, (curW - stageRect.width) / 2);
		let maxOffsetY = Math.max(0, (curH - stageRect.height) / 2);

		translateX = Math.max(-maxOffsetX, Math.min(maxOffsetX, translateX));
		translateY = Math.max(-maxOffsetY, Math.min(maxOffsetY, translateY));
	}

	function zoomToPoint(targetScale, clientX, clientY) {
		const mediaEl = getMediaElement();
		if (!mediaEl) return;
		const stageRect = stage.getBoundingClientRect();
		const cx = clientX !== undefined ? (clientX - stageRect.left - stageRect.width / 2) : 0;
		const cy = clientY !== undefined ? (clientY - stageRect.top - stageRect.height / 2) : 0;

		const prevScale = scale;
		scale = Math.max(1, Math.min(10, targetScale));

		if (scale <= 1.02) {
			resetZoom();
			return;
		}

		isZoomed = true;
		const factor = scale / prevScale;
		translateX = (translateX - cx) * factor + cx;
		translateY = (translateY - cy) * factor + cy;
		clampTranslation();
		updateTransform();
	}

	function resetZoom() {
		isZoomed = false;
		scale = 1;
		translateX = 0;
		translateY = 0;
		updateTransform();
	}

	function toggleZoom(clientX, clientY) {
		if (isZoomed) {
			resetZoom();
		} else {
			const { nw } = getNaturalDimensions();
			const mediaEl = getMediaElement();
			const baseRect = getRenderedMediaRect(stage, mediaEl, currentPost?.width, currentPost?.height);
			let targetScale = 2.5;

			if (currentSettings.zoomMode === "original" && nw && baseRect.width > 0) {
				targetScale = Math.max(1.5, nw / baseRect.width);
			} else if (currentSettings.zoomMode === "2x") {
				targetScale = 2.0;
			} else {
				const stageRect = stage.getBoundingClientRect();
				targetScale = Math.max(2.0, stageRect.width / (baseRect.width || 1));
			}

			targetScale = Math.min(targetScale, 6.0);
			zoomToPoint(targetScale, clientX, clientY);
		}
	}

	function setHdActive(active) {
		if (!hdToggleBtn) return;
		hdToggleBtn.classList.toggle("active", active);
		hdToggleBtn.setAttribute("aria-pressed", active ? "true" : "false");
		const icon = hdToggleBtn.querySelector(".material-symbols-rounded");
		if (icon) {
			icon.textContent = active ? "hd" : "sd";
		} else {
			hdToggleBtn.textContent = active ? "HD" : "Sample";
		}
	}

	function setMedia(post, initialThumbSrc) {
		currentPost = post;
		currentThumbSrc = initialThumbSrc || post.previewUrl || post.sampleUrl;
		resetZoom();

		const ext = (post.ext || "").toLowerCase();
		const isVid = ext === "mp4" || ext === "webm";

		if (isVid) {
			setHdActive(true);
			let video = mediaWrap.querySelector("video");
			if (!video) {
				mediaWrap.innerHTML = "";
				video = document.createElement("video");
				video.className = "detail-media";
				video.controls = true;
				video.autoplay = true;
				video.loop = true;
				video.playsInline = true;
				video.setAttribute("playsinline", "");
				video.setAttribute("webkit-playsinline", "");
				if (currentThumbSrc) {
					video.poster = currentThumbSrc;
				}
				video.src = post.fileUrl;
				mediaWrap.appendChild(video);
			}
			video.play().catch(() => {});
		} else {
			const preferHD = currentSettings.preferHD;
			const displayUrl = (preferHD || !post.sampleUrl) ? post.fileUrl : post.sampleUrl;
			setHdActive(displayUrl === post.fileUrl);

			let fullImg = mediaWrap.querySelector(".img-full");
			let loader = mediaWrap.querySelector(".media-loader");

			if (!fullImg && !mediaWrap.querySelector("img")) {
				mediaWrap.innerHTML = `
					<div class="progressive-media-container" style="--media-ar: ${post.width || 1} / ${post.height || 1};">
						<img class="img-preview" src="${currentThumbSrc || displayUrl}" alt="" referrerpolicy="no-referrer" />
						<div class="media-loader" aria-hidden="true"></div>
						<img class="img-full" src="${displayUrl}" alt="" referrerpolicy="no-referrer" />
					</div>
				`;
				fullImg = mediaWrap.querySelector(".img-full");
				loader = mediaWrap.querySelector(".media-loader");
			}

			if (fullImg) {
				if (fullImg.complete && fullImg.naturalWidth > 0) {
					fullImg.classList.add("is-loaded");
					if (loader) loader.style.display = "none";
				} else {
					fullImg.addEventListener("load", () => {
						fullImg.classList.add("is-loaded");
						if (loader) loader.style.display = "none";
					}, { once: true });
					fullImg.addEventListener("error", () => {
						if (loader) loader.style.display = "none";
					}, { once: true });
				}
			}
		}
	}

	function onPointerDown(e) {
		if (!isMediaActive()) return;
		activePointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

		if (activePointers.size === 1) {
			isDragging = true;
			didDrag = false;
			startPointerX = e.clientX;
			startPointerY = e.clientY;
			startTransX = translateX;
			startTransY = translateY;
			stage.setPointerCapture(e.pointerId);
		} else if (activePointers.size === 2) {
			const pts = Array.from(activePointers.values());
			pinchStartDist = Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y);
			pinchStartScale = scale;
			const stageRect = stage.getBoundingClientRect();
			pinchCenterStageX = ((pts[0].x + pts[1].x) / 2) - stageRect.left - stageRect.width / 2;
			pinchCenterStageY = ((pts[0].y + pts[1].y) / 2) - stageRect.top - stageRect.height / 2;
		}
	}

	function onPointerMove(e) {
		if (!activePointers.has(e.pointerId)) return;
		activePointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

		if (activePointers.size === 2) {
			const pts = Array.from(activePointers.values());
			const curDist = Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y);
			if (pinchStartDist > 0) {
				const newScale = Math.max(1, Math.min(8, pinchStartScale * (curDist / pinchStartDist)));
				scale = newScale;
				isZoomed = scale > 1.05;
				clampTranslation();
				updateTransform();
			}
			didDrag = true;
			return;
		}

		if (isDragging && isZoomed) {
			const dx = e.clientX - startPointerX;
			const dy = e.clientY - startPointerY;
			if (Math.hypot(dx, dy) > 5) {
				didDrag = true;
			}
			translateX = startTransX + dx;
			translateY = startTransY + dy;
			clampTranslation();
			updateTransform();
		}
	}

	function onPointerUp(e) {
		if (activePointers.has(e.pointerId)) {
			try {
				stage.releasePointerCapture(e.pointerId);
			} catch (_) {}
			activePointers.delete(e.pointerId);
		}
		if (activePointers.size === 0) {
			isDragging = false;
			if (scale <= 1.05) {
				resetZoom();
			}
		}
	}

	let lastTapTime = 0;
	function onStageClick(e) {
		if (!isMediaActive()) return;
		if (didDrag) {
			didDrag = false;
			return;
		}

		if (e.target.closest("button, a, input, video")) {
			return;
		}

		const mediaEl = getMediaElement();
		const mediaRect = mediaEl ? getRenderedMediaRect(stage, mediaEl, currentPost?.width, currentPost?.height) : null;
		const clickedOutsideMedia = mediaRect ? (
			e.clientX < mediaRect.left ||
			e.clientX > mediaRect.right ||
			e.clientY < mediaRect.top ||
			e.clientY > mediaRect.bottom
		) : true;

		if (clickedOutsideMedia && onOutsideClick) {
			onOutsideClick();
			return;
		}

		const now = Date.now();
		if (now - lastTapTime < 320) {
			lastTapTime = 0;
			toggleZoom(e.clientX, e.clientY);
		} else {
			lastTapTime = now;
		}
	}

	function onWheel(e) {
		if (!isMediaActive()) return;
		if (e.ctrlKey || e.metaKey || isZoomed) {
			e.preventDefault();
			const delta = -e.deltaY;
			const zoomFactor = delta > 0 ? 1.15 : 0.87;
			zoomToPoint(scale * zoomFactor, e.clientX, e.clientY);
		}
	}

	function onZoomToggleClick(e) {
		e.stopPropagation();
		toggleZoom();
	}

	function onHdToggleClick(e) {
		e.stopPropagation();
		if (!currentPost || isVideo()) return;
		const fullImg = mediaWrap.querySelector(".img-full") || mediaWrap.querySelector("img");
		if (!fullImg) return;

		const isCurrentlyHD = hdToggleBtn?.classList.contains("active");
		const targetUrl = isCurrentlyHD ? (currentPost.sampleUrl || currentPost.fileUrl) : currentPost.fileUrl;
		setHdActive(!isCurrentlyHD);

		if (fullImg.src !== targetUrl) {
			const full = new Image();
			full.src = targetUrl;
			full.onload = () => {
				fullImg.src = targetUrl;
				fullImg.classList.add("is-loaded");
			};
		}
	}

	stage.addEventListener("pointerdown", onPointerDown);
	stage.addEventListener("pointermove", onPointerMove);
	stage.addEventListener("pointerup", onPointerUp);
	stage.addEventListener("pointercancel", onPointerUp);
	stage.addEventListener("click", onStageClick);
	stage.addEventListener("wheel", onWheel, { passive: false });
	zoomToggleBtn?.addEventListener("click", onZoomToggleClick);
	hdToggleBtn?.addEventListener("click", onHdToggleClick);

	return {
		setMedia,
		resetZoom,
		destroy: () => {
			stage.removeEventListener("pointerdown", onPointerDown);
			stage.removeEventListener("pointermove", onPointerMove);
			stage.removeEventListener("pointerup", onPointerUp);
			stage.removeEventListener("pointercancel", onPointerUp);
			stage.removeEventListener("click", onStageClick);
			stage.removeEventListener("wheel", onWheel);
			zoomToggleBtn?.removeEventListener("click", onZoomToggleClick);
			hdToggleBtn?.removeEventListener("click", onHdToggleClick);
		},
		isZoomed: () => isZoomed
	};
}

let currentDetailViewer = null;
let overlayToken = 0;
let overlayFeedTitle = "";
let overlayReturnFocus = null;

function setSheetExpanded(expanded) {
	const sidebar = document.getElementById("post-sidebar");
	const handleZone = document.getElementById("sheet-handle-zone");
	const backdrop = document.getElementById("sheet-backdrop");
	if (sidebar) {
		sidebar.classList.toggle("is-expanded", expanded);
	}
	if (backdrop) {
		backdrop.classList.toggle("is-active", expanded);
	}
	if (handleZone) {
		handleZone.setAttribute("aria-expanded", expanded ? "true" : "false");
		const chevron = handleZone.querySelector(".sheet-chevron");
		if (chevron) {
			chevron.textContent = expanded ? "expand_more" : "expand_less";
		}
	}
}

function closePostOverlay(fromPopState = false) {
	if (!document.body.classList.contains("has-post-overlay")) return;

	overlayToken++;

	if (currentDetailViewer) {
		currentDetailViewer.destroy();
		currentDetailViewer = null;
	}

	const slot = document.getElementById("post-overlay-slot");
	if (slot) {
		slot.innerHTML = "";
	}
	document.body.classList.remove("has-post-overlay");

	const mainEl = document.querySelector("main");
	const topbarEl = document.querySelector(".topbar");
	if (mainEl) mainEl.inert = false;
	if (topbarEl) topbarEl.inert = false;

	if (overlayFeedTitle) {
		document.title = overlayFeedTitle;
		overlayFeedTitle = "";
	}

	if (overlayReturnFocus) {
		overlayReturnFocus.focus({ preventScroll: true });
		overlayReturnFocus = null;
	}

	updateSingleColumnAutoplay();

	if (!fromPopState && window.history.state?.postOverlay) {
		window.history.back();
	}
}

async function openPostOverlay(targetUrl, initialThumbSrc, { push = true } = {}) {
	const slot = document.getElementById("post-overlay-slot");
	if (!slot) return;

	const token = ++overlayToken;

	if (!document.body.classList.contains("has-post-overlay")) {
		overlayFeedTitle = document.title;
		overlayReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
	}

	// Pause inline autoplay videos
	document.querySelectorAll(".post-card .card-media-link video.inline-autoplay-video").forEach((vid) => {
		vid.pause();
	});

	document.body.classList.add("has-post-overlay");

	const mainEl = document.querySelector("main");
	const topbarEl = document.querySelector(".topbar");
	if (mainEl) mainEl.inert = true;
	if (topbarEl) topbarEl.inert = true;

	if (push) {
		window.history.pushState({ postOverlay: true, url: targetUrl }, "", targetUrl);
	}

	// Immediate subtle loading state / thumbnail if available
	if (initialThumbSrc) {
		slot.innerHTML = `
			<div class="post-detail" id="post-detail">
				<div class="post-detail-nav">
					<button type="button" class="page-btn post-back-btn" id="post-back-btn">← Back to Feed</button>
					<button type="button" class="icon-btn post-close-btn" id="post-close-btn" aria-label="Close preview">
						<span class="material-symbols-rounded" aria-hidden="true">close</span>
					</button>
				</div>
				<div class="post-detail-grid">
					<div class="post-media-column">
						<div class="post-media-box media-viewer-stage" id="post-stage">
							<div class="media-wrap">
								<div class="progressive-media-container">
									<img class="img-preview" src="${initialThumbSrc}" alt="" referrerpolicy="no-referrer" />
									<div class="media-loader" aria-hidden="true"></div>
								</div>
							</div>
						</div>
					</div>
				</div>
			</div>`;
	}

	try {
		const res = await fetch(targetUrl);
		if (token !== overlayToken) return;

		if (!res.ok) {
			window.location.href = targetUrl;
			return;
		}
		const html = await res.text();
		if (token !== overlayToken) return;

		const doc = new DOMParser().parseFromString(html, "text/html");
		const detail = doc.getElementById("post-detail");
		if (!detail) {
			window.location.href = targetUrl;
			return;
		}

		const pageTitle = doc.querySelector("title")?.textContent;
		if (pageTitle) {
			document.title = pageTitle;
		}

		slot.innerHTML = detail.outerHTML;
		if (window.htmx) {
			window.htmx.process(slot);
		}
		initPostDetail();
		document.getElementById("post-close-btn")?.focus({ preventScroll: true });
	} catch (err) {
		if (token !== overlayToken) return;
		console.error("Failed to load post detail:", err);
		window.location.href = targetUrl;
	}
}

function initBottomSheet() {
	const sidebar = document.getElementById("post-sidebar");
	const handleZone = document.getElementById("sheet-handle-zone");
	const backdrop = document.getElementById("sheet-backdrop");
	const stage = document.getElementById("post-stage");
	if (!sidebar || !handleZone) return;

	let hasMoved = false;

	handleZone.onclick = (e) => {
		if (e.target.closest("a, button")) return;
		if (hasMoved) {
			hasMoved = false;
			return;
		}
		const isExp = sidebar.classList.contains("is-expanded");
		setSheetExpanded(!isExp);
	};

	if (backdrop) {
		backdrop.onclick = (e) => {
			e.stopPropagation();
			setSheetExpanded(false);
		};
	}

	if (stage) {
		stage.addEventListener("click", () => {
			if (sidebar.classList.contains("is-expanded")) {
				setSheetExpanded(false);
			}
		});
	}

	let touchStartY = 0;
	let isDraggingSheet = false;

	handleZone.addEventListener("touchstart", (e) => {
		if (e.touches.length !== 1) return;
		touchStartY = e.touches[0].clientY;
		isDraggingSheet = true;
		hasMoved = false;
		sidebar.style.transition = "none";
	}, { passive: true });

	handleZone.addEventListener("touchmove", (e) => {
		if (!isDraggingSheet || e.touches.length !== 1) return;
		const dy = e.touches[0].clientY - touchStartY;
		if (Math.abs(dy) > 5) {
			hasMoved = true;
		}
		const isExp = sidebar.classList.contains("is-expanded");
		if (isExp && dy > 0) {
			sidebar.style.transform = `translateY(${dy}px)`;
		} else if (!isExp && dy < 0) {
			sidebar.style.transform = `translateY(calc(100% - 3.75rem - env(safe-area-inset-bottom, 0px) + ${dy}px))`;
		}
	}, { passive: true });

	handleZone.addEventListener("touchend", (e) => {
		if (!isDraggingSheet) return;
		isDraggingSheet = false;
		sidebar.style.transition = "";
		sidebar.style.transform = "";
		const dy = (e.changedTouches[0]?.clientY || 0) - touchStartY;
		const isExp = sidebar.classList.contains("is-expanded");
		if (!isExp && dy < -30) {
			setSheetExpanded(true);
		} else if (isExp && dy > 30) {
			setSheetExpanded(false);
		}
	}, { passive: true });

	handleZone.addEventListener("touchcancel", () => {
		if (!isDraggingSheet) return;
		isDraggingSheet = false;
		hasMoved = false;
		sidebar.style.transition = "";
		sidebar.style.transform = "";
	}, { passive: true });

	const sheetBody = sidebar.querySelector(".sheet-body");
	if (sheetBody) {
		let bodyTouchStartY = 0;
		sheetBody.addEventListener("touchstart", (e) => {
			if (e.touches.length === 1 && sheetBody.scrollTop <= 0) {
				bodyTouchStartY = e.touches[0].clientY;
			} else {
				bodyTouchStartY = 0;
			}
		}, { passive: true });

		sheetBody.addEventListener("touchend", (e) => {
			if (bodyTouchStartY && sidebar.classList.contains("is-expanded")) {
				const dy = (e.changedTouches[0]?.clientY || 0) - bodyTouchStartY;
				if (dy > 60 && sheetBody.scrollTop <= 0) {
					setSheetExpanded(false);
				}
			}
			bodyTouchStartY = 0;
		}, { passive: true });

		sheetBody.addEventListener("touchcancel", () => {
			bodyTouchStartY = 0;
		}, { passive: true });
	}
}

function initStageTouchSwipe(stage) {
	let touchStartX = 0;
	let touchStartY = 0;

	stage.addEventListener("touchstart", (e) => {
		if (e.touches.length === 1) {
			touchStartX = e.touches[0].clientX;
			touchStartY = e.touches[0].clientY;
		}
	}, { passive: true });

	stage.addEventListener("touchend", (e) => {
		if (currentDetailViewer?.isZoomed?.()) return;
		if (e.changedTouches.length !== 1) return;
		const dx = e.changedTouches[0].clientX - touchStartX;
		const dy = e.changedTouches[0].clientY - touchStartY;
		if (Math.abs(dx) > 45 && Math.abs(dy) < 90) {
			if (dx < 0) {
				const nextBtn = document.getElementById("post-group-next") || document.getElementById("group-next-btn");
				if (nextBtn && !nextBtn.classList.contains("disabled")) {
					nextBtn.click();
				}
			} else {
				const prevBtn = document.getElementById("post-group-prev") || document.getElementById("group-prev-btn");
				if (prevBtn && !prevBtn.classList.contains("disabled")) {
					prevBtn.click();
				}
			}
		}
	}, { passive: true });
}

function initPostDetail() {
	const postStage = document.getElementById("post-stage");
	if (!postStage) {
		if (currentDetailViewer) {
			currentDetailViewer.destroy();
			currentDetailViewer = null;
		}
		return;
	}

	if (currentDetailViewer) {
		currentDetailViewer.destroy();
		currentDetailViewer = null;
	}

	const mediaWrap = postStage.querySelector(".media-wrap");
	if (!mediaWrap) return;

	const zoomToggleBtn = document.getElementById("post-zoom-btn");
	const hdToggleBtn = document.getElementById("post-hd-toggle");
	const d = postStage.dataset;

	currentDetailViewer = createZoomableMediaViewer({
		stage: postStage,
		mediaWrap: mediaWrap,
		zoomToggleBtn: zoomToggleBtn,
		hdToggleBtn: hdToggleBtn,
		onOutsideClick: () => {
			const sidebar = document.getElementById("post-sidebar");
			if (sidebar?.classList.contains("is-expanded")) {
				setSheetExpanded(false);
				return;
			}
			if (document.body.classList.contains("has-post-overlay")) {
				closePostOverlay(false);
			}
		},
		isMediaActive: () => true
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

	const closeBtn = document.getElementById("post-close-btn");
	if (closeBtn) {
		closeBtn.onclick = (e) => {
			e.preventDefault();
			if (document.body.classList.contains("has-post-overlay")) {
				closePostOverlay(false);
			} else {
				const backBtn = document.getElementById("post-back-btn");
				if (backBtn?.href) {
					window.location.href = backBtn.href;
				} else if (window.history.length > 1) {
					window.history.back();
				} else {
					window.location.href = "/";
				}
			}
		};
	}

	const backBtn = document.getElementById("post-back-btn");
	if (backBtn && document.body.classList.contains("has-post-overlay")) {
		backBtn.onclick = (e) => {
			e.preventDefault();
			closePostOverlay(false);
		};
	}

	initBottomSheet();
	initStageTouchSwipe(postStage);
}





function registerServiceWorker() {
	if (!("serviceWorker" in navigator)) return;

	let hadController = !!navigator.serviceWorker.controller;
	let refreshing = false;
	navigator.serviceWorker.addEventListener("controllerchange", () => {
		if (!hadController) {
			hadController = true;
			return;
		}
		if (!refreshing) {
			refreshing = true;
			window.location.reload();
		}
	});

	navigator.serviceWorker.register("/sw.js", { scope: "/" })
		.then((reg) => {
			setInterval(() => {
				reg.update().catch(() => {});
			}, 60 * 60 * 1000);

			document.addEventListener("visibilitychange", () => {
				if (document.visibilityState === "visible") {
					reg.update().catch(() => {});
				}
			});
		})
		.catch((err) => {
			console.warn("ServiceWorker registration failed:", err);
		});
}

function attachGlobalListeners() {
	if (window.__hypnogazeGlobalAttached) return;
	window.__hypnogazeGlobalAttached = true;

	registerServiceWorker();

	window.addEventListener("resize", () => {
		updateTopbarHeight();
		if (window.matchMedia("(min-width: 960px)").matches) {
			const mobileCheckbox = document.getElementById("mobile-filter-state");
			const indexSidebar = document.getElementById("index-sidebar");
			if (mobileCheckbox?.checked || indexSidebar?.classList.contains("is-open")) {
				window.__hypnogazeSyncDrawer?.(false, true);
			}
		}
	}, { passive: true });

	window.addEventListener("scroll", () => {
		const topbar = document.querySelector(".topbar");
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
			document.body.classList.add("topbar-hidden");
			document.body.classList.remove("topbar-overlay");
		} else if (scrollY < lastScrollY - 2) {
			document.body.classList.remove("topbar-hidden");
			if (scrollY > 10) {
				document.body.classList.add("topbar-overlay");
			} else {
				document.body.classList.remove("topbar-overlay");
			}
		}

		lastScrollY = scrollY;
	}, { passive: true });

	window.addEventListener("popstate", (e) => {
		const open = document.body.classList.contains("has-post-overlay");
		if (open && !e.state?.postOverlay) {
			closePostOverlay(true);
		} else if (!open && e.state?.postOverlay && e.state.url) {
			openPostOverlay(e.state.url, "", { push: false });
		}
		const mobileCheckbox = document.getElementById("mobile-filter-state");
		const indexSidebar = document.getElementById("index-sidebar");
		if (mobileCheckbox?.checked || indexSidebar?.classList.contains("is-open")) {
			window.__hypnogazeSyncDrawer?.(false, true);
		}
	});

	window.addEventListener("keydown", (e) => {
		if (document.activeElement && (document.activeElement.tagName === "INPUT" || document.activeElement.tagName === "TEXTAREA")) return;

		if (e.key === "Escape") {
			const sidebar = document.getElementById("post-sidebar");
			if (sidebar?.classList.contains("is-expanded")) {
				e.preventDefault();
				setSheetExpanded(false);
				return;
			}

			if (document.body.classList.contains("has-post-overlay")) {
				e.preventDefault();
				closePostOverlay(false);
				return;
			}

			const mobileCheckbox = document.getElementById("mobile-filter-state");
			const indexSidebar = document.getElementById("index-sidebar");
			if (mobileCheckbox?.checked || indexSidebar?.classList.contains("is-open")) {
				e.preventDefault();
				window.__hypnogazeSyncDrawer?.(false);
			}
			return;
		}

		if (e.key === "ArrowLeft") {
			const prevBtn = document.getElementById("post-group-prev") || document.getElementById("group-prev-btn");
			if (prevBtn && !prevBtn.classList.contains("disabled")) {
				e.preventDefault();
				prevBtn.click();
			}
		} else if (e.key === "ArrowRight") {
			const nextBtn = document.getElementById("post-group-next") || document.getElementById("group-next-btn");
			if (nextBtn && !nextBtn.classList.contains("disabled")) {
				e.preventDefault();
				nextBtn.click();
			}
		}
	});

	document.addEventListener("click", (e) => {
		if (e.target.id === "post-overlay-slot") {
			closePostOverlay(false);
			return;
		}

		const cardLink = e.target.closest(".card-media-link, .card-id");
		if (cardLink) {
			if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
			if (e.target.closest("video.inline-autoplay-video")) return;
			if (currentSettings.enableLightbox === false) return;

			e.preventDefault();
			const card = cardLink.closest(".post-card");
			const thumbImg = card?.querySelector(".card-thumb");
			const thumbSrc = thumbImg?.currentSrc || thumbImg?.src || "";
			openPostOverlay(cardLink.href, thumbSrc);
			return;
		}

		document.querySelectorAll(".tag-suggestions").forEach((box) => {
			const parent = box.parentElement;
			if (parent && !parent.contains(e.target)) {
				box.innerHTML = "";
			}
		});

		const trigger = e.target.closest("[data-tooltip]");
		document.querySelectorAll("[data-tooltip].is-tooltip-open").forEach((openEl) => {
			if (openEl !== trigger) openEl.classList.remove("is-tooltip-open");
		});
		if (trigger) {
			e.stopPropagation();
			trigger.classList.toggle("is-tooltip-open");
		}
	});

	document.addEventListener("keydown", (e) => {
		if (e.key === "Escape") {
			document.querySelectorAll("[data-tooltip].is-tooltip-open").forEach((el) => {
				el.classList.remove("is-tooltip-open");
			});
		}
	});

	document.addEventListener("htmx:afterSwap", (e) => {
		const target = e.detail?.target;
		if (target?.id === "post-detail" || target?.closest?.("#post-detail")) {
			const detail = target.id === "post-detail" ? target : target.closest("#post-detail");
			const stage = detail?.querySelector("#post-stage");
			const newID = stage?.dataset?.id;
			const newUrl = newID ? `/posts/${newID}${window.location.search}` : e.detail?.xhr?.responseURL;
			if (newUrl) {
				const inOverlay = document.body.classList.contains("has-post-overlay");
				window.history.replaceState(inOverlay ? { postOverlay: true, url: newUrl } : null, "", newUrl);
			}
			if (newID) {
				document.title = `Post #${newID} — Hypnogaze`;
			}
			initPostDetail();
		}
	});

	document.addEventListener("htmx:historyRestore", () => {
		initPostDetail();
	});
}

function hypnogazeInit() {
	updateTopbarHeight();
	applyVisualSettings(currentSettings);
	initSettingsUI();
	initSearchUI();
	initPostDetail();
	updateSingleColumnAutoplay();
}

window.hypnogazeInit = hypnogazeInit;

attachGlobalListeners();
hypnogazeInit();
