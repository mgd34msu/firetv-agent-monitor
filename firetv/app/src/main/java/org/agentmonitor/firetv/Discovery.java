package org.agentmonitor.firetv;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.net.SocketTimeoutException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/**
 * The display side of LAN discovery (docs/http-api.md#discovery): broadcast
 * "rokuagents-discover v1 &lt;token id&gt; &lt;nonce&gt;" and accept the first
 * "rokuagents v1 &lt;url&gt; &lt;mac&gt;" whose mac proves the sender holds the token.
 */
final class Discovery {
    private static final int WAIT_MS = 1500;

    private Discovery() {}

    /** The helper's base url, or "" when nobody answered in time. */
    static String find(String token, int port) {
        // The helper ignores short tokens: they could be guessed from their id.
        if (token == null || token.length() < 32 || port <= 0 || port > 65535) {
            return "";
        }
        String nonce = randomHex(8);
        byte[] request = ("rokuagents-discover v1 " + tokenId(token) + " " + nonce).getBytes(StandardCharsets.US_ASCII);
        try (DatagramSocket socket = new DatagramSocket()) {
            socket.setBroadcast(true);
            for (InetAddress host : broadcastAddresses()) {
                try {
                    socket.send(new DatagramPacket(request, request.length, host, port));
                } catch (Exception ignored) {
                    // one interface failing does not stop the others
                }
            }
            long deadline = System.currentTimeMillis() + WAIT_MS;
            byte[] buf = new byte[512];
            while (true) {
                long left = deadline - System.currentTimeMillis();
                if (left <= 0) {
                    return "";
                }
                socket.setSoTimeout((int) left);
                DatagramPacket reply = new DatagramPacket(buf, buf.length);
                try {
                    socket.receive(reply);
                } catch (SocketTimeoutException e) {
                    return "";
                }
                String url = verifiedUrl(new String(reply.getData(), 0, reply.getLength(), StandardCharsets.US_ASCII), token, nonce);
                if (!url.isEmpty()) {
                    return url;
                }
            }
        } catch (Exception e) {
            return "";
        }
    }

    private static String verifiedUrl(String reply, String token, String nonce) {
        String[] parts = reply.trim().split(" ");
        if (parts.length != 4 || !parts[0].equals("rokuagents") || !parts[1].equals("v1")) {
            return "";
        }
        String url = parts[2];
        if (!url.startsWith("http://")) {
            return "";
        }
        return parts[3].equalsIgnoreCase(sign(token, nonce + " " + url)) ? url : "";
    }

    private static List<InetAddress> broadcastAddresses() throws Exception {
        List<InetAddress> hosts = new ArrayList<>();
        hosts.add(InetAddress.getByName("255.255.255.255"));
        for (NetworkInterface nic : Collections.list(NetworkInterface.getNetworkInterfaces())) {
            if (!nic.isUp() || nic.isLoopback()) {
                continue;
            }
            for (InterfaceAddress a : nic.getInterfaceAddresses()) {
                if (a.getBroadcast() != null && !hosts.contains(a.getBroadcast())) {
                    hosts.add(a.getBroadcast());
                }
            }
        }
        return hosts;
    }

    private static String tokenId(String token) {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(token.getBytes(StandardCharsets.US_ASCII));
            return hex(digest).substring(0, 16);
        } catch (Exception e) {
            return "";
        }
    }

    private static String sign(String token, String message) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(token.getBytes(StandardCharsets.US_ASCII), "HmacSHA256"));
            return hex(mac.doFinal(message.getBytes(StandardCharsets.US_ASCII))).substring(0, 32);
        } catch (Exception e) {
            return "-";
        }
    }

    private static String randomHex(int bytes) {
        byte[] b = new byte[bytes];
        new SecureRandom().nextBytes(b);
        return hex(b);
    }

    private static String hex(byte[] bytes) {
        StringBuilder sb = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) {
            sb.append(String.format("%02x", b & 0xff));
        }
        return sb.toString();
    }
}
