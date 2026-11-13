(() => {
  "use strict";

  const MAX_FILES = 20;
  const MAX_FILE_SIZE = 100 * 1024 * 1024;
  const allowedExtensions = new Set(["jpg", "jpeg", "png", "webp", "heic", "heif", "mp4", "mov"]);
  const typeByExtension = {
    jpg: "image/jpeg",
    jpeg: "image/jpeg",
    png: "image/png",
    webp: "image/webp",
    heic: "image/heic",
    heif: "image/heif",
    mp4: "video/mp4",
    mov: "video/quicktime",
  };

  const params = new URLSearchParams(window.location.search);
  const token = params.get("t") || "";
  const tableName = params.get("n") || "";
  const config = window.WEDDING_CONFIG || {};

  const elements = {
    title: document.querySelector("#title"),
    intro: document.querySelector("#intro"),
    tableLabel: document.querySelector("#table-label"),
    invalidToken: document.querySelector("#invalid-token"),
    uploadArea: document.querySelector("#upload-area"),
    input: document.querySelector("#file-input"),
    selection: document.querySelector("#selection"),
    selectionCount: document.querySelector("#selection-count"),
    fileList: document.querySelector("#file-list"),
    clearButton: document.querySelector("#clear-button"),
    uploadButton: document.querySelector("#upload-button"),
    status: document.querySelector("#status"),
    statusText: document.querySelector("#status-text"),
    progressBar: document.querySelector("#progress-bar"),
    error: document.querySelector("#error"),
    success: document.querySelector("#success"),
    moreButton: document.querySelector("#more-button"),
  };

  let selectedFiles = [];
  let uploading = false;

  elements.title.textContent = config.title || "Unsere Hochzeit";
  elements.intro.textContent = config.intro || "Teile deine schönsten Fotos und Videos mit uns.";
  if (tableName) elements.tableLabel.textContent = tableName;

  if (!/^[A-Za-z0-9_-]{12,128}$/.test(token)) {
    elements.invalidToken.classList.remove("hidden");
    elements.uploadArea.classList.add("hidden");
  }

  function extensionOf(fileName) {
    return String(fileName).split(".").pop().toLowerCase();
  }

  function contentTypeOf(file) {
    const ext = extensionOf(file.name);
    return typeByExtension[ext] || file.type;
  }

  function isSupported(file) {
    return allowedExtensions.has(extensionOf(file.name));
  }

  function formatBytes(bytes) {
    if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }

  function setError(message = "") {
    elements.error.textContent = message;
    elements.error.classList.toggle("hidden", !message);
  }

  function resetStatus() {
    elements.status.classList.add("hidden");
    elements.progressBar.style.width = "0%";
    elements.statusText.textContent = "";
  }

  function renderSelection() {
    const count = selectedFiles.length;
    elements.selection.classList.toggle("hidden", count === 0);
    elements.uploadButton.disabled = count === 0 || uploading;
    elements.selectionCount.textContent = `${count} ${count === 1 ? "Datei" : "Dateien"} ausgewählt`;
    elements.fileList.replaceChildren();

    for (const file of selectedFiles) {
      const item = document.createElement("li");
      const name = document.createElement("span");
      const size = document.createElement("span");
      name.className = "file-name";
      size.className = "file-size";
      name.textContent = file.name;
      size.textContent = formatBytes(file.size);
      item.append(name, size);
      elements.fileList.append(item);
    }
  }

  function chooseFiles(fileList) {
    setError();
    resetStatus();
    const files = Array.from(fileList || []);
    if (files.length > MAX_FILES) {
      setError(`Bitte höchstens ${MAX_FILES} Dateien auf einmal auswählen.`);
      selectedFiles = [];
    } else if (files.some((file) => file.size < 1 || file.size > MAX_FILE_SIZE)) {
      setError("Mindestens eine Datei ist leer oder größer als 100 MB.");
      selectedFiles = [];
    } else if (files.some((file) => !isSupported(file))) {
      setError("Mindestens ein Dateityp wird nicht unterstützt.");
      selectedFiles = [];
    } else {
      selectedFiles = files;
    }
    renderSelection();
  }

  async function requestUploadUrls(files) {
    const response = await fetch("/api/upload-url", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      cache: "no-store",
      body: JSON.stringify({
        token,
        files: files.map((file, index) => ({
          id: String(index),
          name: file.name,
          size: file.size,
          type: contentTypeOf(file),
        })),
      }),
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.message || "Der Upload konnte nicht vorbereitet werden.");
    return body.uploads;
  }

  function putFile(file, upload, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", upload.url);
      xhr.setRequestHeader("Content-Type", upload.contentType);
      xhr.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable) onProgress(event.loaded);
      });
      xhr.addEventListener("load", () => {
        if (xhr.status >= 200 && xhr.status < 300) resolve();
        else reject(new Error(`S3 antwortet mit Status ${xhr.status}.`));
      });
      xhr.addEventListener("error", () => reject(new Error("Netzwerkfehler beim Hochladen.")));
      xhr.addEventListener("abort", () => reject(new Error("Upload wurde abgebrochen.")));
      xhr.send(file);
    });
  }

  async function runWithConcurrency(items, concurrency, worker) {
    let next = 0;
    const runners = Array.from({ length: Math.min(concurrency, items.length) }, async () => {
      while (next < items.length) {
        const index = next++;
        await worker(items[index], index);
      }
    });
    await Promise.all(runners);
  }

  async function upload() {
    if (uploading || selectedFiles.length === 0) return;
    uploading = true;
    setError();
    elements.uploadButton.disabled = true;
    elements.clearButton.disabled = true;
    elements.input.disabled = true;
    elements.status.classList.remove("hidden");
    elements.statusText.textContent = "Upload wird vorbereitet …";

    try {
      const uploads = await requestUploadUrls(selectedFiles);
      const byId = new Map(uploads.map((item) => [item.id, item]));
      const loaded = new Array(selectedFiles.length).fill(0);
      const totalBytes = selectedFiles.reduce((sum, file) => sum + file.size, 0);
      let completed = 0;

      const updateProgress = () => {
        const uploadedBytes = loaded.reduce((sum, value) => sum + value, 0);
        const percent = Math.min(100, Math.round((uploadedBytes / totalBytes) * 100));
        elements.progressBar.style.width = `${percent}%`;
        elements.statusText.textContent = `${completed} von ${selectedFiles.length} Dateien hochgeladen · ${percent}%`;
      };

      await runWithConcurrency(selectedFiles, 3, async (file, index) => {
        const uploadTarget = byId.get(String(index));
        if (!uploadTarget) throw new Error("Für eine Datei fehlt die Upload-Adresse.");
        await putFile(file, uploadTarget, (bytes) => {
          loaded[index] = bytes;
          updateProgress();
        });
        loaded[index] = file.size;
        completed += 1;
        updateProgress();
      });

      elements.uploadArea.classList.add("hidden");
      elements.success.classList.remove("hidden");
    } catch (error) {
      setError(error?.message || "Der Upload ist fehlgeschlagen. Bitte erneut versuchen.");
      elements.statusText.textContent = "Upload unterbrochen";
    } finally {
      uploading = false;
      elements.uploadButton.disabled = selectedFiles.length === 0;
      elements.clearButton.disabled = false;
      elements.input.disabled = false;
    }
  }

  function reset() {
    selectedFiles = [];
    elements.input.value = "";
    elements.success.classList.add("hidden");
    elements.uploadArea.classList.remove("hidden");
    setError();
    resetStatus();
    renderSelection();
  }

  elements.input.addEventListener("change", (event) => chooseFiles(event.target.files));
  elements.clearButton.addEventListener("click", reset);
  elements.uploadButton.addEventListener("click", upload);
  elements.moreButton.addEventListener("click", reset);
})();

