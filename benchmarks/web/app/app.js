// Task Manager — shared JS logic.
// The HTML pages set window.BUGGY = true/false before loading this script.

(function () {
  "use strict";

  var BUGGY = window.BUGGY || false;
  var tasks = [];
  var nextId = 1;
  var currentUser = null;

  // DOM refs
  var loginPage = document.getElementById("login-page");
  var dashboard = document.getElementById("dashboard");
  var loginForm = document.getElementById("login-form");
  var loginError = document.getElementById("login-error");
  var userDisplay = document.getElementById("user-display");
  var logoutBtn = document.getElementById("logout-btn");
  var newTaskInput = document.getElementById("new-task-input");
  var newTaskPriority = document.getElementById("new-task-priority");
  var addTaskBtn = document.getElementById("add-task-btn");
  var searchInput = document.getElementById("search-input");
  var filterSelect = document.getElementById("filter-select");
  var taskList = document.getElementById("task-list");
  var taskCount = document.getElementById("task-count");
  var statTotal = document.getElementById("stat-total");
  var statCompleted = document.getElementById("stat-completed");
  var statPending = document.getElementById("stat-pending");

  // --- Auth ---
  if (loginForm) {
    loginForm.addEventListener("submit", function (e) {
      e.preventDefault();
      var user = document.getElementById("username").value;
      var pass = document.getElementById("password").value;

      if (user === "admin" && pass === "password") {
        if (BUGGY) {
          // WEB-5: Redirect goes to wrong page (404)
          window.location.href = "/nonexistent.html";
          return;
        }
        currentUser = user;
        loginPage.style.display = "none";
        dashboard.classList.add("active");
        userDisplay.textContent = "Logged in as " + user;
        addSampleTasks();
        render();
        return;
      }

      if (BUGGY) {
        // WEB-1: Error message never appears (swallowed)
        return;
      }
      loginError.textContent = "Invalid username or password";
      loginError.style.display = "block";
    });
  }

  if (logoutBtn) {
    logoutBtn.addEventListener("click", function () {
      currentUser = null;
      tasks = [];
      nextId = 1;
      dashboard.classList.remove("active");
      loginPage.style.display = "";
      loginError.style.display = "none";
    });
  }

  // --- Sample data ---
  function addSampleTasks() {
    tasks = [
      { id: nextId++, text: "Review pull request #42", priority: "high", done: false },
      { id: nextId++, text: "Update API documentation", priority: "medium", done: false },
      { id: nextId++, text: "Fix login page styling", priority: "low", done: true },
      { id: nextId++, text: "Deploy to staging", priority: "high", done: false },
      { id: nextId++, text: "Write unit tests for auth module", priority: "medium", done: false },
    ];
  }

  // --- Stats ---
  function updateStats() {
    if (BUGGY) {
      // WEB-2: Stats don't update after initial load (stale state)
      statTotal.textContent = "0";
      statCompleted.textContent = "0";
      statPending.textContent = "0";
      return;
    }
    var completed = tasks.filter(function (t) { return t.done; }).length;
    statTotal.textContent = tasks.length;
    statCompleted.textContent = completed;
    statPending.textContent = tasks.length - completed;
  }

  // --- Filtering ---
  function getFilteredTasks() {
    var search = (searchInput ? searchInput.value : "").toLowerCase();
    var filter = filterSelect ? filterSelect.value : "all";

    return tasks.filter(function (t) {
      if (filter === "pending" && t.done) return false;
      if (filter === "completed" && !t.done) return false;
      if (search && t.text.toLowerCase().indexOf(search) === -1) return false;
      return true;
    });
  }

  // --- Rendering ---
  function render() {
    updateStats();
    var filtered = getFilteredTasks();

    taskList.innerHTML = "";
    if (filtered.length === 0) {
      taskList.innerHTML = '<div class="empty-state">No tasks found</div>';
      taskCount.textContent = "";
      return;
    }

    filtered.forEach(function (task) {
      var div = document.createElement("div");
      div.className = "task-item";
      div.setAttribute("data-id", task.id);

      var checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.checked = task.done;
      checkbox.addEventListener("change", function () {
        task.done = checkbox.checked;
        if (BUGGY) {
          // WEB-3: Completing task doesn't update visual state (no strikethrough)
          render(); // re-render but the class won't be applied below
          return;
        }
        render();
      });

      var span = document.createElement("span");
      span.className = "task-text";
      if (!BUGGY && task.done) {
        // WEB-3: In buggy mode, completed class is never applied
        span.classList.add("completed");
      }
      span.textContent = task.text;

      var priority = document.createElement("span");
      priority.className = "task-priority " + task.priority;
      priority.textContent = task.priority.charAt(0).toUpperCase() + task.priority.slice(1);

      var delBtn = document.createElement("button");
      delBtn.className = "delete-btn";
      delBtn.textContent = "\u00d7";
      if (!BUGGY) {
        // WEB-4 (clean): Delete works
        delBtn.addEventListener("click", function () {
          tasks = tasks.filter(function (t) { return t.id !== task.id; });
          render();
        });
      }
      // WEB-4 (buggy): No click handler — button does nothing

      div.appendChild(checkbox);
      div.appendChild(span);
      div.appendChild(priority);
      div.appendChild(delBtn);
      taskList.appendChild(div);
    });

    taskCount.textContent = filtered.length + " of " + tasks.length + " tasks shown";
  }

  // --- Add task ---
  function addTask() {
    if (!newTaskInput) return;
    var text = newTaskInput.value.trim();
    if (!text) return;
    tasks.push({
      id: nextId++,
      text: text,
      priority: newTaskPriority ? newTaskPriority.value : "medium",
      done: false,
    });
    newTaskInput.value = "";
    render();
  }

  if (addTaskBtn) addTaskBtn.addEventListener("click", addTask);
  if (newTaskInput) {
    newTaskInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter") addTask();
    });
  }
  if (searchInput) searchInput.addEventListener("input", render);
  if (filterSelect) filterSelect.addEventListener("change", render);
})();
