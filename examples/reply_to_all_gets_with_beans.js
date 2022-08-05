function CMD_GET(packet) {
    log("have some beans!");
    reply({
        Magic: 0x81,
        Command: packet.Command,
        Datatype: 0,
        Status: 0,
        Opaque: packet.Opaque,
        Cas: 3,
        CollectionID: packet.CollectionID,
        Key: packet.Key,
        Value: stringToBytes("\"beans\""),
        Extras: [0, 0, 0, 0]
    });
}
