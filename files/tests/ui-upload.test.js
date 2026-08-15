// @vitest-environment jsdom
/**
 * Ticket #77 — Files plugin: upload from OS + file drop.
 *
 * The UI module (app.js) bootstraps on import, so these tests install the full
 * element set from index.html first, stub window.shell.callTool, then drive
 * the exported handlers directly (upload via direct call, drop via a real
 * dispatched drop event through the drop zone wiring).
 *
 * window.shell.callTool is invoked as callTool(pluginId, name, args) — the
 * first argument is the plugin id, so assertions destructure `[, name, args]`.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

// jsdom's File lacks arrayBuffer(). Polyfill it so uploadFile() can read
// file contents the same way it does in a real browser.
if (typeof File !== "undefined" && !File.prototype.arrayBuffer) {
  File.prototype.arrayBuffer = async function () {
    const reader = new FileReader();
    return new Promise((resolve, reject) => {
      reader.onload = () => resolve(reader.result);
      reader.onerror = () => reject(reader.error);
      reader.readAsArrayBuffer(this);
    });
  };
}

const ELEMENT_IDS = [
  "files-workspace",
  "root-caption",
  "root-button",
  "search-form",
  "search-input",
  "refresh-button",
  "up-button",
  "new-file-button",
  "upload-button",
  "upload-input",
  "tree-container",
  "collapse-tree-button",
  "breadcrumbs",
  "folder-title",
  "listing-count",
  "listing-body",
  "new-folder-button",
  "selection-actions",
  "rename-button",
  "delete-button",
  "preview-pane",
  "preview-icon",
  "preview-title",
  "preview-subtitle",
  "preview-meta",
  "preview-content",
  "close-preview-button",
  "files-drop-overlay",
  "modal-overlay",
  "modal-title",
  "modal-description",
  "modal-form",
  "modal-name-field",
  "modal-input",
  "modal-textarea",
  "modal-content-field",
  "modal-field-label",
  "confirm-copy",
  "modal-error",
  "modal-cancel-button",
  "modal-save-button",
  "modal-close-button",
  "toast-container",
];

function installDom() {
  document.body.innerHTML = `
    <header>
      <button id="upload-button" type="button"></button>
      <input id="upload-input" type="file" multiple>
      <button id="refresh-button" type="button"></button>
      <button id="up-button" type="button"></button>
      <button id="new-file-button" type="button"></button>
    </header>
    <main id="files-workspace">
      <aside><button id="root-button" type="button"></button><button id="collapse-tree-button" type="button"></button><nav id="tree-container"></nav></aside>
      <section>
        <form id="search-form"><input id="search-input"></form>
        <nav id="breadcrumbs"></nav>
        <h1 id="folder-title"></h1>
        <span id="listing-count"></span>
        <button id="new-folder-button" type="button"></button>
        <span id="selection-actions" hidden><button id="rename-button" type="button"></button><button id="delete-button" type="button"></button></span>
        <div id="listing-body"></div>
      </section>
      <section id="preview-pane" hidden>
        <span id="preview-icon"></span><strong id="preview-title"></strong><span id="preview-subtitle"></span>
        <div id="preview-meta"></div><div id="preview-content"></div>
        <button id="close-preview-button" type="button"></button>
      </section>
    </main>
    <div id="files-drop-overlay" hidden><strong class="files-drop-label" data-drop-label></strong></div>
    <div id="modal-overlay" hidden>
      <form id="modal-form"><input id="modal-input"><textarea id="modal-textarea"></textarea><span id="modal-error"></span><button id="modal-save-button" type="submit"></button><button id="modal-cancel-button" type="button"></button><button id="modal-close-button" type="button"></button><span id="modal-title"></span><span id="modal-description"></span><span id="modal-field-label"></span><label id="modal-name-field"></label><label id="modal-content-field" hidden></label><p id="confirm-copy" hidden></p></form>
    </div>
    <div id="toast-container"></div>
  `;
  window.shell = { callTool: vi.fn() };
  const rootCap = document.createElement("span");
  rootCap.id = "root-caption";
  document.querySelector("aside")?.append(rootCap);
  for (const id of ELEMENT_IDS) {
    if (!document.getElementById(id)) {
      const el = document.createElement("div");
      el.id = id;
      el.hidden = true;
      document.body.append(el);
    }
  }
}

let app;
let callTool;

async function loadApp() {
  app = await import("../ui/app.js");
  return app;
}

function makeFile(name, bytes, opts = {}) {
  return new File([bytes], name, opts);
}

function makeDropEvent(dataTransfer) {
  const event = new Event("drop", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "dataTransfer", { value: dataTransfer, configurable: true });
  return event;
}

function writeCalls() {
  return callTool.mock.calls.filter(([, name]) => name === "write").map(([, , args]) => args);
}

describe("Files UI — upload & drop (ticket #77)", () => {
  beforeEach(async () => {
    vi.resetModules();
    app = undefined;
    installDom();
    const mod = await loadApp();
    callTool = window.shell.callTool;
    callTool.mockImplementation(async (_pluginId, name) => {
      if (name === "list") return { items: [] };
      if (name === "tree") return { tree: [] };
      return {};
    });
    mod.initDropZone();
  });

  it("uploads a binary file as base64 via write with encoding=base64", async () => {
    const bytes = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0xff]);
    const file = makeFile("shot.png", bytes, { type: "image/png" });
    await app.handleUpload([file]);

    const writes = writeCalls();
    expect(writes).toHaveLength(1);
    expect(writes[0].path).toBe("shot.png");
    expect(writes[0].encoding).toBe("base64");
    expect(Buffer.from(writes[0].content, "base64")).toEqual(Buffer.from(bytes));
  });

  it("uploads a text file as utf8 (no encoding) so it stays previewable", async () => {
    const file = makeFile("hello.txt", "hello world", { type: "text/plain" });
    await app.handleUpload([file]);

    const writes = writeCalls();
    expect(writes).toHaveLength(1);
    expect(writes[0].path).toBe("hello.txt");
    expect(writes[0].encoding).toBeUndefined();
    expect(writes[0].content).toBe("hello world");
  });

  it("skips files larger than 10 MiB with an error toast", async () => {
    const big = makeFile("big.bin", new Uint8Array(10 * 1024 * 1024 + 1));
    await app.handleUpload([big]);

    expect(writeCalls()).toHaveLength(0);
    const toasts = [...document.querySelectorAll("#toast-container .toast")].map((t) => t.textContent);
    expect(toasts.some((t) => t.includes("larger than 10 MiB"))).toBe(true);
  });

  it("prevents default and uploads files dropped on the window", async () => {
    const file = makeFile("drop.txt", "from drop", { type: "text/plain" });
    const dataTransfer = { types: ["Files"], files: [file], items: [], dropEffect: "" };
    const event = makeDropEvent(dataTransfer);
    document.dispatchEvent(event);

    // The drop handler is async (handleFilesDrop → handleUpload → uploadFile
    // → file.arrayBuffer via FileReader → callTool). FileReader.onload is a
    // macrotask, so wait with setTimeout instead of microtask polling.
    for (let i = 0; i < 20 && writeCalls().length === 0; i++) {
      await new Promise((r) => setTimeout(r, 10));
    }
    const writes = writeCalls();
    expect(writes.some((w) => w.path === "drop.txt")).toBe(true);
  });

  it("shows the drop overlay during dragenter and hides on drop", () => {
    const overlayEl = document.getElementById("files-drop-overlay");
    expect(overlayEl.hidden).toBe(true);
    const enter = new Event("dragenter", { bubbles: true, cancelable: true });
    Object.defineProperty(enter, "dataTransfer", { value: { types: ["Files"], files: [], items: [], dropEffect: "" }, configurable: true });
    document.dispatchEvent(enter);
    expect(overlayEl.hidden).toBe(false);
    const drop = makeDropEvent({ types: ["Files"], files: [makeFile("x.txt", "x")], items: [], dropEffect: "" });
    document.dispatchEvent(drop);
    expect(overlayEl.hidden).toBe(true);
  });

  it("uploads the contents of a dropped folder preserving relative paths", async () => {
    // Fake webkitGetAsEntry hierarchy: projects/{readme.md, src/index.js}
    const leafFile = (name, content) => ({
      isFile: true,
      isDirectory: false,
      name,
      file: (cb) => cb(new File([content], name, { type: "text/plain" })),
    });
    const dirFile = (name, children) => ({
      isFile: false,
      isDirectory: true,
      name,
      createReader: () => {
        let served = false;
        return {
          readEntries: (cb) => {
            if (served) return cb([]);
            served = true;
            return cb(children);
          },
        };
      },
    });
    const projectDir = dirFile("projects", [
      leafFile("readme.md", "# hi"),
      dirFile("src", [leafFile("index.js", "export {}")]),
    ]);
    const item = { webkitGetAsEntry: () => projectDir, getAsFile: () => null };
    const dataTransfer = { types: ["Files"], files: [], items: [item], dropEffect: "" };
    await app.handleFilesDrop(makeDropEvent(dataTransfer));

    const writes = writeCalls();
    const paths = writes.map((w) => w.path);
    expect(paths).toContain("readme.md");
    expect(paths).toContain("src/index.js");
    // Nesting is preserved below the dropped folder.
    expect(writes.find((w) => w.path === "src/index.js").content).toBe("export {}");
  });
});