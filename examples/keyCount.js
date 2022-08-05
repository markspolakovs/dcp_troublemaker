var keyCounts = {};

function CMD_GET(packet) {
    if (packet.Magic !== 0x80) {
        forward(packet);
        return;
    }
    var key = bytesToString(packet.Key)
    if (!(key in keyCounts)) {
        keyCounts[key] = 0;
    }
    keyCounts[key]++;
    log("Got a GET for key " + key + " " + keyCounts[key] + " times");
    forward(packet);
}
