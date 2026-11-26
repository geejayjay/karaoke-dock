package app.karaokedock.mobile;

import android.os.Bundle;
import android.webkit.JavascriptInterface;
import android.webkit.WebSettings;
import com.getcapacitor.BridgeActivity;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.net.SocketTimeoutException;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.Map;
import org.json.JSONArray;
import org.json.JSONObject;

public class MainActivity extends BridgeActivity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        if (getBridge() != null && getBridge().getWebView() != null) {
            WebSettings settings = getBridge().getWebView().getSettings();
            settings.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
            settings.setDomStorageEnabled(true);
            getBridge().getWebView().addJavascriptInterface(new LanBridge(), "KaraokeDockNative");
        }
    }

    public static class LanBridge {
        @JavascriptInterface
        public String discover(int port) {
            int targetPort = port > 0 ? port : 7464;
            Map<String, JSONObject> found = new LinkedHashMap<>();
            byte[] probe = "KARAOKEDOCK?".getBytes(StandardCharsets.UTF_8);
            try (DatagramSocket sock = new DatagramSocket()) {
                sock.setBroadcast(true);
                sock.setSoTimeout(250);
                sendProbe(sock, probe, InetAddress.getByName("255.255.255.255"), targetPort);
                for (NetworkInterface nif : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                    if (!nif.isUp() || nif.isLoopback()) continue;
                    for (InterfaceAddress ia : nif.getInterfaceAddresses()) {
                        InetAddress bcast = ia.getBroadcast();
                        if (bcast != null) sendProbe(sock, probe, bcast, targetPort);
                    }
                }
                long deadline = System.currentTimeMillis() + 650;
                byte[] buf = new byte[512];
                while (System.currentTimeMillis() < deadline) {
                    try {
                        DatagramPacket pkt = new DatagramPacket(buf, buf.length);
                        sock.receive(pkt);
                        String text = new String(pkt.getData(), 0, pkt.getLength(), StandardCharsets.UTF_8);
                        JSONObject obj = new JSONObject(text);
                        if (!"karaokedock".equals(obj.optString("app"))) continue;
                        int srvPort = obj.optInt("port", targetPort);
                        String ip = pkt.getAddress().getHostAddress();
                        String origin = "http://" + ip + ":" + srvPort;
                        JSONObject item = new JSONObject();
                        item.put("origin", origin);
                        item.put("name", obj.optString("name", ip));
                        item.put("roomRequired", obj.optBoolean("roomRequired", false));
                        found.put(origin, item);
                    } catch (SocketTimeoutException ignored) {
                    }
                }
            } catch (Exception ignored) {
            }
            JSONArray out = new JSONArray();
            for (JSONObject v : found.values()) out.put(v);
            return out.toString();
        }

        private static void sendProbe(DatagramSocket sock, byte[] data, InetAddress addr, int port) {
            try {
                sock.send(new DatagramPacket(data, data.length, addr, port));
            } catch (Exception ignored) {
            }
        }
    }
}
