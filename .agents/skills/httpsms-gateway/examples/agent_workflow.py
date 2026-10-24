#!/usr/bin/env python3
"""
agent_workflow.py - Example of integrating HttpSMS Gateway with an AI Agent.
Demonstrates function calling definitions and automated response generation.
"""

import json
import os
import sys

# Define standard tool declarations (compatible with OpenAI, Gemini, Claude, and LangChain)
HTTPSMS_TOOL_DECLARATIONS = [
    {
        "type": "function",
        "function": {
            "name": "send_sms",
            "description": "Send an SMS text message to a user via the HttpSMS cellular gateway.",
            "parameters": {
                "type": "object",
                "properties": {
                    "recipient": {
                        "type": "string",
                        "description": "Destination phone number in international E.164 format (e.g., +255797455136)."
                    },
                    "body": {
                        "type": "string",
                        "description": "The message body to deliver."
                    },
                    "sim_slot": {
                        "type": "integer",
                        "description": "SIM slot index (0 for primary, 1 for secondary).",
                        "default": 0
                    }
                },
                "required": ["recipient", "body"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "check_device_telemetry",
            "description": "Check if gateway cellular phones are online, their battery level, and SIM signal quality.",
            "parameters": {
                "type": "object",
                "properties": {}
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "get_conversation_history",
            "description": "Fetch recent inbound and outbound messages with a specific customer phone number.",
            "parameters": {
                "type": "object",
                "properties": {
                    "phone_number": {
                        "type": "string",
                        "description": "Customer phone number to filter history by."
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Number of recent messages to retrieve (default: 10).",
                        "default": 10
                    }
                },
                "required": ["phone_number"]
            }
        }
    }
]

def simulate_agent_execution():
    print("==================================================")
    print("  AI Agent Tool Execution Pattern for HttpSMS     ")
    print("==================================================")
    print("\n[1] Tool Schemas available to Agent:")
    for tool in HTTPSMS_TOOL_DECLARATIONS:
        fn = tool["function"]
        print(f"  - {fn['name']}: {fn['description']}")
    
    print("\n[2] Agent receives user prompt:")
    prompt = "A customer (+255797455136) asked about store hours. Check their last message and reply saying we are open 9am - 8pm."
    print(f"  User: \"{prompt}\"")
    
    print("\n[3] Model issues tool call:")
    tool_call = {
        "tool": "get_conversation_history",
        "arguments": {"phone_number": "+255797455136", "limit": 3}
    }
    print(f"  Call: {json.dumps(tool_call)}")
    
    print("\n[4] Gateway provides message context, and Agent dispatches reply:")
    dispatch_call = {
        "tool": "send_sms",
        "arguments": {
            "recipient": "+255797455136",
            "body": "Hello! Our store is open Monday through Saturday from 9:00 AM to 8:00 PM. Let us know if you need anything else!",
            "sim_slot": 0
        }
    }
    print(f"  Dispatch: {json.dumps(dispatch_call, indent=4)}")
    print("\n[✓] Message routed to Android gateway handset and delivered over cellular network.")

if __name__ == "__main__":
    simulate_agent_execution()
