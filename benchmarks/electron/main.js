const { app, BrowserWindow, ipcMain } = require("electron");
const path = require("path");
const fs = require("fs");

const mode = process.argv.includes("--mode")
  ? process.argv[process.argv.indexOf("--mode") + 1]
  : "clean";

let mainWindow;

app.whenReady().then(() => {
  mainWindow = new BrowserWindow({
    width: 900,
    height: 700,
    title: `Notes (${mode})`,
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  mainWindow.loadFile("index.html", { query: { mode } });
});

app.on("window-all-closed", () => app.quit());

// IPC handlers for note persistence
const notesFile = path.join(app.getPath("userData"), "notes.json");

function loadNotes() {
  try {
    return JSON.parse(fs.readFileSync(notesFile, "utf-8"));
  } catch {
    return [];
  }
}

function saveNotes(notes) {
  fs.writeFileSync(notesFile, JSON.stringify(notes, null, 2));
}

ipcMain.handle("notes:load", () => loadNotes());

ipcMain.handle("notes:save", (_event, notes) => {
  if (mode === "buggy") {
    // ELEC-4: Save silently drops data — writes empty array
    saveNotes([]);
    return true;
  }
  saveNotes(notes);
  return true;
});
