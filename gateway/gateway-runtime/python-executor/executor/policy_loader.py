# Copyright (c) 2025, WSO2 LLC. (http://www.w3.org) All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Policy discovery and import — stateless function-based."""

import importlib
import logging
import os
import sys
from typing import Any, Callable, Dict, Optional, Tuple

from sdk.policy import OnRequestFn, OnResponseFn, PolicyMetadata

logger = logging.getLogger(__name__)


class PolicyLoader:
    """Loads Python policies from the policy registry.

    At startup, the Python Executor needs to know which policies exist.
    The Gateway Builder generates a `python_policy_registry.py` file that maps
    policy names to their module import paths.

    The loader now looks for on_request() and on_response() functions directly
    in the policy module, rather than factory functions that create class instances.
    """

    def __init__(self, registry_path: Optional[str] = None):
        """Initialize the policy loader.

        Args:
            registry_path: Path to the policy registry module.
                          Defaults to looking for 'python_policy_registry' in PYTHONPATH.
        """
        self._functions: Dict[str, Tuple[Optional[OnRequestFn], Optional[OnResponseFn]]] = {}
        self._loaded = False
        self._registry_path = registry_path

    def load_policies(self) -> int:
        """Load all policies from the registry.

        Returns:
            Number of policies loaded
        """
        if self._loaded:
            return len(self._functions)

        # Add parent directory to path for imports
        executor_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        if executor_dir not in sys.path:
            sys.path.insert(0, executor_dir)

        # Try to import the registry
        try:
            # First try the generated registry
            from python_policy_registry import PYTHON_POLICIES
            registry = PYTHON_POLICIES
            logger.info(f"Loaded policy registry with {len(registry)} entries")
        except ImportError:
            logger.warning("No policy registry found (python_policy_registry.py). "
                          "No Python policies will be available.")
            registry = {}

        for policy_key, module_path in registry.items():
            self._load_policy(policy_key, module_path)

        self._loaded = True
        return len(self._functions)

    def _load_policy(self, policy_key: str, module_path: str) -> bool:
        """Load a single policy from its module.

        Args:
            policy_key: Policy identifier (name:version)
            module_path: Python module path (e.g., "policies.my_policy.policy")

        Returns:
            True if loaded successfully
        """
        try:
            # Import the module
            module = importlib.import_module(module_path)

            # Look for on_request and on_response functions
            has_on_request = hasattr(module, 'on_request')
            has_on_response = hasattr(module, 'on_response')

            if has_on_request or has_on_response:
                # New function-based pattern
                on_request_fn = getattr(module, 'on_request', None)
                on_response_fn = getattr(module, 'on_response', None)

                # Validate they are callable
                if on_request_fn is not None and not callable(on_request_fn):
                    logger.error(f"Policy module {module_path} 'on_request' is not callable")
                    return False
                if on_response_fn is not None and not callable(on_response_fn):
                    logger.error(f"Policy module {module_path} 'on_response' is not callable")
                    return False

                self._functions[policy_key] = (on_request_fn, on_response_fn)
                logger.info(f"Loaded policy: {policy_key} from {module_path} (function-based)")
                return True

            # Backward compatibility: look for get_policy factory function
            if hasattr(module, 'get_policy'):
                logger.warning(
                    f"Policy {policy_key} uses deprecated class-based pattern. "
                    "Migrate to on_request()/on_response() functions."
                )
                factory = getattr(module, 'get_policy')
                if not callable(factory):
                    logger.error(f"Policy module {module_path} 'get_policy' is not callable")
                    return False

                # Create a dummy instance to extract the methods
                dummy_metadata = PolicyMetadata()
                dummy_instance = factory(dummy_metadata, {})

                on_request_fn = dummy_instance.on_request
                on_response_fn = dummy_instance.on_response

                self._functions[policy_key] = (on_request_fn, on_response_fn)
                logger.info(f"Loaded policy: {policy_key} from {module_path} (deprecated class-based)")
                return True

            logger.error(
                f"Policy module {module_path} does not export 'on_request'/'on_response' "
                "functions or 'get_policy' factory"
            )
            return False

        except Exception as e:
            logger.error(f"Failed to load policy {policy_key} from {module_path}: {e}")
            return False

    def get_policy_functions(
        self,
        name: str,
        version: str
    ) -> Tuple[Optional[OnRequestFn], Optional[OnResponseFn]]:
        """Get the on_request and on_response functions for a policy.

        Args:
            name: Policy name
            version: Policy version

        Returns:
            Tuple of (on_request_fn, on_response_fn). Either can be None if not implemented.

        Raises:
            KeyError: If policy not found
        """
        # Try exact version first
        key = f"{name}:{version}"
        if key in self._functions:
            return self._functions[key]

        # Try major version only (e.g., "my-policy:v1.0.0" -> "my-policy:v1")
        major_version = version.split('.')[0] if '.' in version else version
        key_major = f"{name}:{major_version}"
        if key_major in self._functions:
            return self._functions[key_major]

        raise KeyError(f"Policy not found: {name}:{version}")

    def get_loaded_policies(self) -> list:
        """Get list of loaded policy keys.

        Returns:
            List of policy keys (name:version strings)
        """
        return list(self._functions.keys())

    def get_loaded_policy_count(self) -> int:
        """Get the number of loaded policies.

        Returns:
            Number of successfully loaded policies
        """
        return len(self._functions)
