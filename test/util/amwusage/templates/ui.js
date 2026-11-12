"use strict";
document.querySelectorAll("[data-filter]").forEach(function(input) {
  input.addEventListener("input", function() {
    var query = input.value.toLowerCase();
    input.closest("details").querySelectorAll("tbody tr").forEach(function(row) {
      row.hidden = !row.textContent.toLowerCase().includes(query);
    });
  });
});
document.getElementById("download").addEventListener("click", function() {
  var evidence = document.getElementById("evidence").textContent;
  var url = URL.createObjectURL(new Blob([evidence], {type: "application/json"}));
  var link = document.createElement("a");
  link.href = url;
  link.download = "amw-evidence.json";
  link.click();
  setTimeout(function() { URL.revokeObjectURL(url); }, 1000);
});
