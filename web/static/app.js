(function () {
  const reloadHint = "Перезагрузите страницу и попробуйте снова.";

  document.addEventListener("DOMContentLoaded", function () {
    bindRemoteForms(document);
  });

  function bindRemoteForms(root) {
    root.querySelectorAll('form[method="post"]').forEach(function (form) {
      if (form.dataset.remoteBound === "true") {
        return;
      }
      form.dataset.remoteBound = "true";
      form.addEventListener("submit", function (event) {
        handleFormSubmit(event, form);
      });
    });
  }

  async function handleFormSubmit(event, form) {
    event.preventDefault();
    clearPageAlert();
    setFormPending(form, true);

    try {
      const response = await submitForm(form);
      await handleResponse(form, response);
    } catch (error) {
      showError("Не удалось выполнить запрос. " + reloadHint, true);
    } finally {
      setFormPending(form, false);
    }
  }

  function submitForm(form) {
    const action = form.getAttribute("action") || window.location.href;
    const isMultipart = isMultipartForm(form);
    const body = isMultipart
      ? new FormData(form)
      : new URLSearchParams(new FormData(form));

    return fetch(action, {
      method: "POST",
      body: body,
      credentials: "same-origin",
      headers: {
        "X-Requested-With": "fetch",
      },
    });
  }

  async function handleResponse(form, response) {
    if (response.redirected) {
      handleSuccess(form, response.url);
      return;
    }

    const contentType = response.headers.get("content-type") || "";

    if (!response.ok) {
      const message = await extractErrorMessage(response, contentType);
      showError(appendReloadHint(message), true);
      return;
    }

    if (contentType.includes("text/html")) {
      const html = await response.text();
      const message = extractAlertMessage(html) || "Операция не выполнена.";
      showError(message, false);
      return;
    }

    handleSuccess(form, response.url || window.location.href);
  }

  function handleSuccess(form, nextURL) {
    if (isAttachmentDeleteForm(form) && isSameLocation(nextURL, window.location.href)) {
      removeAttachmentRow(form);
      showToast("Файл удален.", "info");
      return;
    }

    if (nextURL) {
      if (isSameLocation(nextURL, window.location.href)) {
        window.location.reload();
        return;
      }
      window.location.href = nextURL;
      return;
    }

    window.location.reload();
  }

  async function extractErrorMessage(response, contentType) {
    const body = await response.text();
    if (contentType.includes("text/html")) {
      return extractAlertMessage(body) || "Ошибка при выполнении операции.";
    }

    const text = body.trim();
    return text || "Ошибка при выполнении операции.";
  }

  function extractAlertMessage(html) {
    const parser = new DOMParser();
    const documentNode = parser.parseFromString(html, "text/html");
    const alert = documentNode.querySelector(".alert-error, .alert-info");
    if (!alert) {
      return "";
    }
    return (alert.textContent || "").trim();
  }

  function appendReloadHint(message) {
    if (!message) {
      return reloadHint;
    }
    if (message.includes(reloadHint)) {
      return message;
    }
    return message + " " + reloadHint;
  }

  function showError(message, withToast) {
    renderPageAlert("error", message);
    if (withToast) {
      showToast(message, "error");
    }
  }

  function renderPageAlert(kind, message) {
    clearPageAlert();

    const main = document.querySelector("main.container");
    if (!main) {
      return;
    }

    const alert = document.createElement("div");
    alert.className = "alert js-page-alert " + (kind === "error" ? "alert-error" : "alert-info");
    alert.textContent = message;
    main.prepend(alert);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function clearPageAlert() {
    document.querySelectorAll(".js-page-alert").forEach(function (node) {
      node.remove();
    });
  }

  function showToast(message, kind) {
    const host = ensureToastHost();
    const toast = document.createElement("div");
    toast.className = "toast toast-" + kind;

    const text = document.createElement("div");
    text.className = "toast-text";
    text.textContent = message;

    const close = document.createElement("button");
    close.type = "button";
    close.className = "toast-close";
    close.setAttribute("aria-label", "Закрыть уведомление");
    close.textContent = "×";
    close.addEventListener("click", function () {
      toast.remove();
    });

    toast.appendChild(text);
    toast.appendChild(close);
    host.appendChild(toast);

    const timeout = kind === "error" ? 9000 : 4000;
    window.setTimeout(function () {
      toast.remove();
    }, timeout);
  }

  function ensureToastHost() {
    let host = document.getElementById("toast-host");
    if (host) {
      return host;
    }

    host = document.createElement("div");
    host.id = "toast-host";
    host.className = "toast-host";
    host.setAttribute("aria-live", "polite");
    host.setAttribute("aria-atomic", "true");
    document.body.appendChild(host);
    return host;
  }

  function setFormPending(form, pending) {
    const buttons = form.querySelectorAll('button[type="submit"], input[type="submit"]');
    buttons.forEach(function (button) {
      button.disabled = pending;
    });
  }

  function isMultipartForm(form) {
    const enctype = (form.getAttribute("enctype") || "").toLowerCase();
    return enctype === "multipart/form-data" || form.querySelector('input[type="file"]') !== null;
  }

  function isAttachmentDeleteForm(form) {
    const action = form.getAttribute("action") || "";
    return /\/requests\/\d+\/attachments\/\d+\/delete\/?$/.test(action);
  }

  function isSameLocation(left, right) {
    if (!left || !right) {
      return false;
    }

    const leftURL = new URL(left, window.location.origin);
    const rightURL = new URL(right, window.location.origin);
    return leftURL.pathname === rightURL.pathname && leftURL.search === rightURL.search;
  }

  function removeAttachmentRow(form) {
    const row = form.closest(".attachment-row");
    const list = form.closest(".attachments");
    if (row) {
      row.remove();
    }
    if (!list) {
      return;
    }
    if (list.querySelector(".attachment-row")) {
      return;
    }

    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "Файлы пока не прикреплены.";
    list.replaceWith(empty);
  }
})();
