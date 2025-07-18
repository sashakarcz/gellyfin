function restartJob() {
    const terminal = document.getElementById('terminal');
    const button = document.querySelector('button');
    
    terminal.innerHTML = 'Restarting Jellyfin job...\n';
    button.disabled = true;
    button.textContent = 'Restarting...';
    
    fetch('/restart', {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json'
        }
    })
    .then(response => {
        if (!response.ok) {
            return response.json().then(errorData => {
                throw new Error(`${errorData.message || 'Unknown error'}`);
            });
        }
        return response.json();
    })
    .then(data => {
        if (data.success) {
            terminal.innerHTML += `✅ ${data.message}\n`;
            terminal.innerHTML += `Job: ${data.job}\n`;
            terminal.innerHTML += `Duration: ${data.duration}\n`;
            if (data.output) {
                terminal.innerHTML += `\nOutput:\n${data.output}\n`;
            }
        } else {
            terminal.innerHTML += `❌ Restart failed\n`;
        }
    })
    .catch(error => {
        console.error('Error:', error);
        terminal.innerHTML += `❌ Error: ${error.message}\n`;
    })
    .finally(() => {
        button.disabled = false;
        button.textContent = 'Restart Jellyfin Job';
    });
}