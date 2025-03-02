function restartJob() {
    const terminal = document.getElementById('terminal');
    terminal.innerHTML = 'Restarting Jellyfin job...\n';
    fetch('/restart')
        .then(response => {
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }
            return response.text();
        })
        .then(data => {
            terminal.innerHTML += data;
        })
        .catch(error => {
            console.error('Error:', error);
            terminal.innerHTML += `Error: ${error}\n`;
        });
}
