// New API key form: the date field matters only when "on a date" is chosen.
const select = document.getElementById("key-expires");
const date = document.getElementById("key-expires-on");
if (select && date) {
  const sync = () => {
    const on = select.value === "date";
    date.hidden = !on;
    date.querySelector("input")?.toggleAttribute("required", on);
  };
  select.addEventListener("change", sync);
  sync();
}
