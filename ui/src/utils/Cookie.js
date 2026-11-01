export function getCookie(name) {
    const value = `; ${document.cookie}`;
    const parts = value.split(`; ${name}=`);
    if (parts.length === 2) return parts.pop().split(";").shift();
}

export function setCookie(name, value) {
    const date = new Date();
    date.setFullYear(date.getFullYear() + 9999);
    const expires = "expires=" + date.toUTCString();
    document.cookie = name + "=" + value + ";" + expires + ";path=/";
}

// 🐱 prompt to initialize/set the backend server URL
export function setUrlPopup() {
    let dialogData = prompt("后端服务地址", getCookie("pbUrl"));
    if (dialogData !== null && dialogData.trim() !== "") {
        setCookie("pbUrl", dialogData);
    } else if (dialogData === "") {
        setCookie("pbUrl", import.meta.env.PB_BACKEND_URL);
    }
    location.reload();
}
