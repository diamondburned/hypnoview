const queryError = document.querySelector("#query-error");
const queryResult = document.querySelector("#query-result");
const generateButtons = document.querySelectorAll("#query-generate .time-period-buttons button");
const openHypnohubButton = document.querySelector("#open-hypnohub");
const copyQueryResultButton = document.querySelector("#copy-query-result");
const helpButton = document.querySelector("#help-button");

window.copyInput = function (selector) {
  const input = document.querySelector(selector);
  if (!input || input.disabled) {
    return;
  }
  input.select();
  document.execCommand("copy");
};

async function generateButtonPress(button) {
  const period = button.id;

  queryResult.value = "";
  queryError.textContent = "";
  disableAllButtons();

  try {
    const response = await fetch(`/api/popular/${period}`);
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    queryResult.value = await response.text();
    button.dataset.chosen = true;
    document.location.hash = period;
  } catch (err) {
    console.error(err);
    queryError.textContent = err.message;
  } finally {
    disableAllButtons(false);
  }
}

function disableAllButtons(disabled = true) {
  for (const button of generateButtons) {
    button.disabled = disabled;
    button.dataset.chosen = undefined;
  }
  queryResult.disabled = disabled;
}

for (const button of generateButtons) {
  if (!["daily", "weekly", "monthly"].includes(button.id)) {
    throw new Error("Invalid generateButton ID");
  }
  button.addEventListener("click", () => generateButtonPress(button));
}

copyQueryResultButton.addEventListener("click", () => window.copyInput("#query-result"));

openHypnohubButton.addEventListener("click", () => {
  if (queryResult.value == "") {
    return;
  }

  const query = queryResult.value;
  const params = new URLSearchParams();
  params.set("page", "post");
  params.set("tags", query);
  params.set("s", "list");

  const url = `https://hypnohub.net/index.php?${params.toString()}`;
  window.open(url, "_blank");
});

helpButton.addEventListener("click", (ev) => {
  if (document.location.hash == "#about") {
    ev.preventDefault();
    document.location.hash = "";
  }
});
