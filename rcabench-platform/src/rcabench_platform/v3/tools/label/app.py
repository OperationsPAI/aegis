import subprocess
import sys
from datetime import datetime
from pathlib import Path
from typing import Any

import polars as pl
import streamlit as st

from rcabench_platform.v2.tools.label.config import (
    APP_ICON,
    APP_TITLE,
    DATASET_BASE_PATH,
    DATASET_CONVERTED_SUFFIX,
    LAYOUT,
)
from rcabench_platform.v2.tools.label.logs_search import LogsSearcher
from rcabench_platform.v2.tools.label.metrics_viz import MetricsVisualizer
from rcabench_platform.v2.tools.label.traces_viz import TracesVisualizer
from rcabench_platform.v2.tools.label.utils import get_data_loader, get_label_manager


def build_dataset_path(datapack_name: str) -> str:
    return str(DATASET_BASE_PATH / datapack_name / DATASET_CONVERTED_SUFFIX)


def get_current_dataset_path() -> str:
    if hasattr(st.session_state, "datapack_name") and st.session_state.datapack_name:
        return build_dataset_path(st.session_state.datapack_name)
    else:
        return ""


def ensure_dataframe_for_display(data: Any) -> pl.DataFrame:
    if isinstance(data, pl.DataFrame):
        return data
    else:
        return pl.DataFrame(data) if data else pl.DataFrame()


@st.cache_data
def load_dataset_summary(dataset_path: str) -> dict[str, Any]:
    """Cache dataset summary to avoid repeated loading."""
    data_loader = get_data_loader()
    if data_loader.set_dataset_path(dataset_path):
        return data_loader.get_dataset_summary()
    return {}


@st.cache_data
def get_available_data(dataset_path: str) -> tuple[list[str], list[str]]:
    data_loader = get_data_loader()
    data_loader.set_dataset_path(dataset_path)
    return data_loader.get_available_metrics(), data_loader.get_available_services()


@st.cache_data
def load_logs_data(dataset_path: str, data_type: str) -> pl.DataFrame:
    data_loader = get_data_loader()
    data_loader.set_dataset_path(dataset_path)
    return data_loader.get_logs_data(data_type)


@st.cache_data
def search_logs_cached(
    dataset_path: str,
    data_type: str,
    search_term: str,
    use_regex: bool,
    case_sensitive: bool,
    log_level: str,
    service_filter: str,
) -> pl.DataFrame:
    data_loader = get_data_loader()
    data_loader.set_dataset_path(dataset_path)
    searcher = LogsSearcher()
    logs_df = data_loader.get_logs_data(data_type)

    return searcher.search_logs_advanced(
        logs_df,
        search_term=search_term,
        use_regex=use_regex,
        case_sensitive=case_sensitive,
        log_level=log_level,
        service_filter=service_filter,
    )


@st.cache_data
def get_metrics_data_cached(dataset_path: str, services: list[str], metrics: list[str], data_type: str) -> pl.DataFrame:
    """Cache metrics data to improve performance."""
    data_loader = get_data_loader()
    data_loader.set_dataset_path(dataset_path)
    return data_loader.get_metrics_by_service(services, metrics, data_type)


@st.cache_data
def get_traces_data_cached(dataset_path: str, data_type: str) -> pl.DataFrame:
    data_loader = get_data_loader()
    data_loader.set_dataset_path(dataset_path)
    return data_loader.get_traces_data(data_type)


def initialize_session_state() -> None:
    st.session_state.setdefault("dataset_loaded", False)
    st.session_state.setdefault(
        "datapack_name",
        "ts0-mysql-bandwidth-5p8bkc",
    )

    st.session_state.setdefault("logs_search_cache", {})
    st.session_state.setdefault("last_search_params", {})
    st.session_state.setdefault("current_filtered_logs", pl.DataFrame())
    st.session_state.setdefault("logs_stats_cache", {})
    st.session_state.setdefault("data_loading_progress", {})
    st.session_state.setdefault("last_tab_active", "metadata")


def configure_page() -> None:
    """Configure page settings."""
    st.set_page_config(
        page_title=APP_TITLE,
        page_icon=APP_ICON,
        layout=LAYOUT,
        initial_sidebar_state="expanded",
    )

    if "page_configured" not in st.session_state:
        st.session_state.page_configured = True
    initialize_session_state()


def render_sidebar() -> None:
    with st.sidebar.form("dataset_form"):
        datapack_name = st.text_input(
            "Datapack Name",
            value=st.session_state.datapack_name,
            help="Please enter the datapack name (e.g., ts0-mysql-bandwidth-5p8bkc)",
        )

        load_submitted = st.form_submit_button("Load Dataset", type="primary")

        if load_submitted and datapack_name:
            with st.spinner("Loading dataset..."):
                try:
                    dataset_path = build_dataset_path(datapack_name)

                    load_dataset_summary.clear()
                    get_available_data.clear()
                    load_logs_data.clear()
                    search_logs_cached.clear()
                    get_metrics_data_cached.clear()
                    get_traces_data_cached.clear()

                    st.session_state.logs_search_cache = {}
                    st.session_state.last_search_params = {}
                    st.session_state.current_filtered_logs = pl.DataFrame()
                    st.session_state.logs_stats_cache = {}

                    summary = load_dataset_summary(dataset_path)
                    if summary:
                        st.session_state.dataset_loaded = True
                        st.session_state.datapack_name = datapack_name
                        st.session_state.dataset_path = dataset_path
                        st.sidebar.success("Dataset loaded successfully!")
                        st.rerun()
                    else:
                        st.session_state.dataset_loaded = False
                        st.sidebar.error("Dataset loading failed, please check the datapack name and file integrity")
                except Exception as e:
                    st.session_state.dataset_loaded = False
                    st.sidebar.error(f"Error loading dataset: {str(e)}")

    if st.session_state.dataset_loaded:
        try:
            dataset_path = build_dataset_path(st.session_state.datapack_name)
            summary = load_dataset_summary(dataset_path)
            if summary:
                st.sidebar.subheader("Dataset Information")
                st.sidebar.metric("Datapack", st.session_state.datapack_name)
                st.sidebar.metric("Namespace", summary.get("namespace", "Unknown"))
                st.sidebar.metric("Fault Type", summary.get("fault_type", "Unknown"))
                st.sidebar.metric("Metrics Count", summary.get("metrics_count", 0))
                st.sidebar.metric("Logs Count", summary.get("logs_count", 0))
                st.sidebar.metric("Traces Count", summary.get("traces_count", 0))

                # Label management
                render_label_management(summary.get("dataset_path", ""))
        except Exception as e:
            st.sidebar.error(f"Error displaying dataset info: {str(e)}")


def render_label_management(dataset_path: str) -> None:
    st.sidebar.subheader("Label Management")

    label_manager = get_label_manager()

    # Get all labels
    all_labels = label_manager.get_all_labels()

    if len(all_labels) > 0:
        # Display current dataset labels
        current_labels = label_manager.get_dataset_labels(dataset_path)
        if len(current_labels) > 0:
            st.sidebar.write("Current Labels:")
            # Use columns to display labels more efficiently
            for label in current_labels.iter_rows(named=True):
                st.sidebar.markdown(
                    f"<span style='background-color: {label['color']}; "
                    f"color: white; padding: 2px 6px; border-radius: 3px; font-size: 12px; margin-right: 4px;'>"
                    f"{label['name']}</span>",
                    unsafe_allow_html=True,
                )

        # Label selection form
        with st.sidebar.form("label_assignment_form"):
            label_options = [(row["name"], row["id"]) for row in all_labels.iter_rows(named=True)]
            selected_labels = st.multiselect(
                "Select Labels",
                options=[name for name, _ in label_options],
                default=(
                    [label["name"] for label in current_labels.iter_rows(named=True)] if len(current_labels) > 0 else []
                ),
            )

            label_submitted = st.form_submit_button("Save Labels")

            if label_submitted:
                selected_ids = [id for name, id in label_options if name in selected_labels]
                if label_manager.assign_labels_to_dataset(dataset_path, selected_ids):
                    st.sidebar.success("Labels saved successfully!")

    # Create new label form
    with st.sidebar.expander("Create New Label"):
        with st.form("new_label_form"):
            new_label_name = st.text_input("Label Name")
            new_label_desc = st.text_area("Label Description", height=60)
            new_label_color = st.color_picker("Label Color", "#007bff")

            add_label_submitted = st.form_submit_button("Add Label")

            if add_label_submitted:
                if new_label_name.strip():
                    if label_manager.add_label(new_label_name, new_label_desc, new_label_color):
                        st.success("Label added successfully!")
                else:
                    st.error("Please enter a label name")


def render_metadata_tab() -> None:
    """Render the metadata tab."""
    if not st.session_state.dataset_loaded:
        st.warning("Please load a dataset in the sidebar first")
        st.info("Enter the dataset path in the sidebar and click 'Load Dataset' to begin.")
        return

    st.header("Metadata Information")

    with st.spinner("Loading metadata..."):
        try:
            data_loader = get_data_loader()
            dataset_path = get_current_dataset_path()
            data_loader.set_dataset_path(dataset_path)

            col1, col2 = st.columns(2)

            with col1:
                st.subheader("Environment Information")
                env_data = data_loader.get_env_data()
                if env_data:
                    st.json(env_data)
                else:
                    st.info("No environment data available")

            with col2:
                st.subheader("Fault Injection Information")
                injection_data = data_loader.get_injection_data()
                if injection_data:
                    # Display key information
                    st.metric("Fault Type", injection_data.get("fault_type", "Unknown"))
                    st.metric("Benchmark", injection_data.get("benchmark", "Unknown"))

                    # Display ground truth information
                    if "ground_truth" in injection_data:
                        st.write("**Ground Truth Information:**")
                        ground_truth = injection_data["ground_truth"]
                        if "service" in ground_truth:
                            st.write(f"- Service: {', '.join(ground_truth['service'])}")
                        if "container" in ground_truth:
                            st.write(f"- Container: {', '.join(ground_truth['container'])}")
                        if "pod" in ground_truth:
                            st.write(f"- Pod: {', '.join(ground_truth['pod'])}")

                    # Complete JSON data
                    with st.expander("Complete Injection Data"):
                        st.json(injection_data)
                else:
                    st.info("No fault injection data available")

            # Conclusion data
            st.subheader("Experiment Conclusion")
            conclusion_data = data_loader.get_conclusion_data()
            if len(conclusion_data) > 0:
                display_data = ensure_dataframe_for_display(conclusion_data)
                st.dataframe(display_data, width="stretch")
            else:
                st.info("No conclusion data available")

        except Exception as e:
            st.error(f"Error loading metadata: {str(e)}")
            st.exception(e)


def render_metrics_tab() -> None:
    """Render the metrics tab."""
    if not st.session_state.dataset_loaded:
        st.warning("Please load a dataset in the sidebar first")
        st.info("Enter the dataset path in the sidebar and click 'Load Dataset' to begin.")
        return

    try:
        dataset_path = get_current_dataset_path()
        available_metrics, available_services = get_available_data(dataset_path)

        if not available_metrics:
            st.warning("No metric data available")
            st.info(
                "This might indicate that the dataset doesn't contain metrics or there's an issue with data loading."
            )
            return

        visualizer = MetricsVisualizer()
        data_loader = get_data_loader()
        data_loader.set_dataset_path(dataset_path)

    except Exception as e:
        st.error(f"Error loading metrics data: {str(e)}")
        return

    data_type = "both"

    if available_services:
        col_svc, col_metric = st.columns([1, 1])

        with col_svc:
            st.write("**Services**")

            # Handle actions that affect widget state before creating widgets
            if "select_all_services_clicked" in st.session_state:
                st.session_state.selected_services = available_services.copy()
                del st.session_state.select_all_services_clicked
                st.rerun()

            if "clear_all_services_clicked" in st.session_state:
                st.session_state.selected_services = []
                del st.session_state.clear_all_services_clicked
                st.rerun()

            if "template_to_load" in st.session_state:
                template_data = st.session_state.template_to_load
                st.session_state.selected_services = template_data["services"]
                st.session_state.selected_metrics_batch = template_data["metrics"]
                del st.session_state.template_to_load
                st.rerun()

            # Default values for multiselect - use session state if available
            if "selected_services" not in st.session_state:
                st.session_state.selected_services = (
                    available_services[:5] if len(available_services) >= 5 else available_services
                )

            selected_services = st.multiselect(
                "Select Services",
                options=available_services,
                help="Select services to view",
                key="selected_services",
            )

            # Helper buttons for services
            svc_btn_col1, svc_btn_col2 = st.columns([1, 1])
            with svc_btn_col1:
                if st.button("Select All", key="select_all_services", width="stretch"):
                    st.session_state.select_all_services_clicked = True
                    st.rerun()
            with svc_btn_col2:
                if st.button("Clear All", key="clear_all_services", width="stretch"):
                    st.session_state.clear_all_services_clicked = True
                    st.rerun()

        # Metrics selection
        with col_metric:
            st.write("**Metrics**")

            # Handle actions that affect widget state before creating widgets
            if "select_all_metrics_clicked" in st.session_state:
                st.session_state.selected_metrics_batch = available_metrics.copy()
                del st.session_state.select_all_metrics_clicked
                st.rerun()

            if "clear_all_metrics_clicked" in st.session_state:
                st.session_state.selected_metrics_batch = []
                del st.session_state.clear_all_metrics_clicked
                st.rerun()

            # Default values for multiselect - use session state if available
            if "selected_metrics_batch" not in st.session_state:
                st.session_state.selected_metrics_batch = (
                    available_metrics[:3] if len(available_metrics) >= 3 else available_metrics
                )

            selected_metrics = st.multiselect(
                "Select Metrics",
                options=available_metrics,
                help="Select metrics to view",
                key="selected_metrics_batch",
            )

            # Helper buttons for metrics
            metric_btn_col1, metric_btn_col2 = st.columns([1, 1])
            with metric_btn_col1:
                if st.button("Select All", key="select_all_metrics", width="stretch"):
                    st.session_state.select_all_metrics_clicked = True
                    st.rerun()
            with metric_btn_col2:
                if st.button("Clear All", key="clear_all_metrics", width="stretch"):
                    st.session_state.clear_all_metrics_clicked = True
                    st.rerun()

        if not selected_services or not selected_metrics:
            st.info("Please select services and metrics to display")
            return

        # Get filtered data
        metrics_df = get_metrics_data_cached(dataset_path, selected_services, selected_metrics, data_type)

        # Display selection summary
        with st.expander("Selection Summary", expanded=False):
            col_summary1, col_summary2 = st.columns(2)
            with col_summary1:
                st.write(f"**Selected Services** ({len(selected_services)} items):")
                st.write(", ".join(selected_services))
            with col_summary2:
                st.write(f"**Selected Metrics** ({len(selected_metrics)} items):")
                st.write(", ".join(selected_metrics))

        # Template management - Streamlit-native approach
        with st.expander("Selection Templates", expanded=False):
            label_manager = get_label_manager()

            col_template1, col_template2 = st.columns(2)

            with col_template1:
                st.subheader("Save Current Selection")
                with st.form("save_template_form"):
                    template_name = st.text_input("Template Name")
                    save_submitted = st.form_submit_button("Save Template")

                    if save_submitted and template_name.strip():
                        if label_manager.save_selection_template(template_name, selected_services, selected_metrics):
                            st.success("Template saved successfully!")
                            st.rerun()

            with col_template2:
                st.subheader("Load Template")

                if "templates_list" not in st.session_state or st.session_state.get("templates_refresh", False):
                    templates_df = label_manager.get_all_selection_templates()
                    st.session_state.templates_list = templates_df
                    st.session_state.templates_refresh = False
                else:
                    templates_df = st.session_state.templates_list

                if len(templates_df) > 0:
                    template_options = [(row["name"], row["id"]) for row in templates_df.iter_rows(named=True)]

                    col_load, col_delete = st.columns(2)
                    with col_load:
                        with st.form("load_template_form"):
                            selected_template = st.selectbox(
                                "Select Template",
                                options=[name for name, _ in template_options],
                            )
                            load_submitted = st.form_submit_button("Load Template")
                            if load_submitted:
                                selected_id = next(id for name, id in template_options if name == selected_template)
                                template_data = label_manager.load_selection_template(selected_id)
                                if template_data:
                                    # Store template data for next run
                                    st.session_state.template_to_load = template_data
                                    st.rerun()
                                else:
                                    st.error("Failed to load template")

                    with col_delete:
                        with st.form("delete_template_form"):
                            selected_template_delete = st.selectbox(
                                "Select Template",
                                options=[name for name, _ in template_options],
                                key="template_to_delete",
                            )
                            delete_submitted = st.form_submit_button("Delete Template")
                            if delete_submitted:
                                selected_id = next(
                                    id for name, id in template_options if name == selected_template_delete
                                )
                                if label_manager.delete_selection_template(selected_id):
                                    st.success("Template deleted successfully!")
                                    st.session_state.templates_refresh = True
                                    st.rerun()
                                else:
                                    st.error("Failed to delete template")
                else:
                    st.info("No templates available")

    else:
        # Individual selection mode
        st.subheader("Individual Metric Selection")
        selected_metrics = st.multiselect(
            "Select metrics to display",
            options=available_metrics,
            default=available_metrics[:3] if len(available_metrics) >= 3 else available_metrics,
            help="Multiple metrics can be selected for comparison",
            key="selected_metrics_individual",
        )

        if not selected_metrics:
            st.info("Please select metrics to display")
            return

        # Get data for individual mode
        metrics_df = data_loader.get_metrics_data(data_type)
        selected_services = []  # Not used in individual mode

    # Get environment data
    env_data = data_loader.get_env_data()

    if len(metrics_df) == 0:
        st.warning("No metric data available that matches the criteria")
        return

    try:
        fig = visualizer.create_time_series_plot(metrics_df, selected_metrics, env_data)
        st.plotly_chart(fig, width="stretch")

    except Exception as e:
        st.error(f"Error occurred while plotting charts: {str(e)}")


def render_logs_tab():
    """Render the logs tab."""
    if not st.session_state.dataset_loaded:
        st.warning("Please load a dataset in the sidebar first")
        st.info("Enter the dataset path in the sidebar and click 'Load Dataset' to begin.")
        return

    st.header("Logs Search and Browsing")

    try:
        col1, col2, col3, col4 = st.columns([3, 1, 1, 1])

        with col1:
            search_term = st.text_input("Search Keywords", help="Supports regex search", key="search_term")

        with col2:
            use_regex = st.checkbox("Regex", help="Enable regex mode", key="use_regex")

        with col3:
            case_sensitive = st.checkbox("Case Sensitive", key="case_sensitive")

        with col4:
            data_type = st.selectbox(
                "Data Type",
                ["both", "normal", "abnormal"],
                format_func=lambda x: {
                    "both": "All",
                    "normal": "Normal",
                    "abnormal": "Abnormal",
                }[x],
                key="logs_data_type",
            )

        dataset_path = get_current_dataset_path()
        current_search_params = {
            "search_term": search_term,
            "use_regex": use_regex,
            "case_sensitive": case_sensitive,
            "data_type": data_type,
            "dataset_path": dataset_path,
        }

        with st.expander("Advanced Filtering"):
            filter_col1, filter_col2 = st.columns(2)

            with filter_col1:
                cache_key = f"{dataset_path}_{data_type}"
                if cache_key not in st.session_state.logs_stats_cache:
                    logs_df = load_logs_data(dataset_path, data_type)
                    if len(logs_df) == 0:
                        st.warning("No log data available")
                        st.info(
                            "This might indicate that the dataset doesn't contain logs "
                            "or there's an issue with data loading."
                        )
                        return

                    searcher = LogsSearcher()
                    log_stats = searcher.get_log_statistics(logs_df)
                    st.session_state.logs_stats_cache[cache_key] = log_stats
                else:
                    log_stats = st.session_state.logs_stats_cache[cache_key]

                log_levels = list(log_stats.get("log_levels", {}).keys())
                selected_level = st.selectbox("Log Level", ["all"] + log_levels, key="log_level_filter")

            with filter_col2:
                services = sorted(list(log_stats.get("services", {}).keys()))
                selected_service = st.selectbox("Service", ["all"] + services[:10], key="service_filter")

        current_search_params.update(
            {
                "log_level": selected_level,
                "service_filter": selected_service,
            }
        )

        search_params_changed = current_search_params != st.session_state.last_search_params

        if search_params_changed:
            with st.spinner("Searching logs..."):
                filtered_logs = search_logs_cached(
                    dataset_path,
                    data_type,
                    search_term,
                    use_regex,
                    case_sensitive,
                    selected_level,
                    selected_service,
                )
                st.session_state.current_filtered_logs = filtered_logs
                st.session_state.last_search_params = current_search_params.copy()
        else:
            filtered_logs = st.session_state.current_filtered_logs

        if len(filtered_logs) > 0:
            logs_df = load_logs_data(dataset_path, data_type)
            col1, col2, col3 = st.columns(3)
            col1.metric("Total Logs", len(logs_df))
            col2.metric("Filtered Results", len(filtered_logs))
            col3.metric(
                "Match Rate",
                f"{len(filtered_logs) / len(logs_df) * 100:.1f}%" if len(logs_df) > 0 else "0%",
            )

            page_size = st.select_slider("Rows per Page", [10, 25, 50, 100], value=25, key="page_size")
            total_pages = max(1, (len(filtered_logs) - 1) // page_size + 1)

            col1, col2, col3 = st.columns([1, 2, 1])
            with col2:
                current_page = (
                    st.number_input(
                        f"Page (Total {total_pages} pages)",
                        min_value=1,
                        max_value=total_pages,
                        value=1,
                        key="current_page",
                    )
                    - 1
                )

            searcher = LogsSearcher()
            display_df, pagination_info = searcher.create_logs_table(filtered_logs, page_size, current_page)

            logs_container = st.container()
            with logs_container:
                st.dataframe(display_df, width="stretch", height=400)

            st.caption(
                f"Showing {pagination_info['start_idx']}-{pagination_info['end_idx']} "
                f"of {pagination_info['total_logs']} entries"
            )

            # 导出功能
            if st.button("Export Search Results", key="export_logs"):
                csv_data = searcher.export_search_results(filtered_logs)
                timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
                st.download_button(
                    label="Download CSV File",
                    data=csv_data,
                    file_name=f"logs_search_results_{timestamp}.csv",
                    mime="text/csv",
                    key="download_logs",
                )
        else:
            st.info("No matching logs found")

    except Exception as e:
        st.error(f"Error occurred while searching logs: {str(e)}")


def render_traces_tab():
    if not st.session_state.dataset_loaded:
        st.warning("Please load a dataset in the sidebar first")
        st.info("Enter the dataset path in the sidebar and click 'Load Dataset' to begin.")
        return

    try:
        visualizer = TracesVisualizer()

        # Load both normal and abnormal data
        with st.spinner("Loading trace data..."):
            dataset_path = get_current_dataset_path()
            normal_traces_df = get_traces_data_cached(dataset_path, "normal")
            abnormal_traces_df = get_traces_data_cached(dataset_path, "abnormal")

        if len(normal_traces_df) == 0 and len(abnormal_traces_df) == 0:
            st.warning("No trace data available")
            st.info(
                "This might indicate that the dataset doesn't contain traces or there's an issue with data loading."
            )
            return

        st.divider()

        # Analyze services first
        with st.spinner("Analyzing services..."):
            normal_services = visualizer.get_service_list(normal_traces_df) if len(normal_traces_df) > 0 else []
            abnormal_services = visualizer.get_service_list(abnormal_traces_df) if len(abnormal_traces_df) > 0 else []
            all_services = sorted(list(set(normal_services + abnormal_services)))

        if not all_services:
            st.warning("No services found in the trace data")
            return

        # Add service selection controls
        col_select, col_info = st.columns([2, 1])

        with col_select:
            st.subheader("Service Analysis")
            selected_service = st.selectbox(
                "Select Service to Analyze",
                all_services,
                key="selected_service_analysis",
                help="Choose a service to view span aggregation comparison between normal and abnormal periods",
            )

        with col_info:
            if selected_service:
                st.subheader("Service Dependencies")

                # Get upstream and downstream services from both normal and abnormal data
                upstream_services = set()
                downstream_services = set()

                if len(normal_traces_df) > 0:
                    upstream_services.update(visualizer.get_upstream_services(normal_traces_df, selected_service))
                    downstream_services.update(visualizer.get_downstream_services(normal_traces_df, selected_service))

                if len(abnormal_traces_df) > 0:
                    upstream_services.update(visualizer.get_upstream_services(abnormal_traces_df, selected_service))
                    downstream_services.update(visualizer.get_downstream_services(abnormal_traces_df, selected_service))

                # Display upstream services
                if upstream_services:
                    st.write("**Upstream Services:**")
                    for service in sorted(upstream_services):
                        st.write(f"{service}")
                else:
                    st.write("**Upstream Services:** None")

                # Display downstream services
                if downstream_services:
                    st.write("**Downstream Services:**")
                    for service in sorted(downstream_services):
                        st.write(f"{service}")
                else:
                    st.write("**Downstream Services:** None")

        if selected_service:
            # First show comparison summary at the top if both normal and abnormal data exist
            if (
                len(normal_traces_df) > 0
                and selected_service in normal_services
                and len(abnormal_traces_df) > 0
                and selected_service in abnormal_services
            ):
                st.subheader("Comparison Summary")

                try:
                    normal_stats = visualizer.get_span_aggregated_stats(normal_traces_df, selected_service)
                    abnormal_stats = visualizer.get_span_aggregated_stats(abnormal_traces_df, selected_service)

                    if len(normal_stats) > 0 and len(abnormal_stats) > 0:
                        # Calculate overall metrics
                        normal_total_calls = normal_stats["total_calls"].sum()
                        normal_total_errors = normal_stats["error_count"].sum()
                        normal_avg_error_rate = (
                            (normal_total_errors / normal_total_calls * 100) if normal_total_calls > 0 else 0
                        )
                        normal_avg_duration = normal_stats["avg_duration_ms"].mean()

                        abnormal_total_calls = abnormal_stats["total_calls"].sum()
                        abnormal_total_errors = abnormal_stats["error_count"].sum()
                        abnormal_avg_error_rate = (
                            (abnormal_total_errors / abnormal_total_calls * 100) if abnormal_total_calls > 0 else 0
                        )
                        abnormal_avg_duration = abnormal_stats["avg_duration_ms"].mean()

                        # Display comparison metrics
                        col1, col2, col3 = st.columns(3)

                        with col1:
                            calls_change = abnormal_total_calls - normal_total_calls
                            st.metric(
                                "Call Volume Change",
                                f"{calls_change:+,}",
                                f"{abnormal_total_calls:,} vs {normal_total_calls:,}",
                            )

                        with col2:
                            error_rate_change = abnormal_avg_error_rate - normal_avg_error_rate
                            st.metric(
                                "Error Rate Change",
                                f"{error_rate_change:+.2f}%",
                                f"{abnormal_avg_error_rate:.2f}% vs {normal_avg_error_rate:.2f}%",
                            )

                        with col3:
                            if (
                                normal_avg_duration is not None
                                and abnormal_avg_duration is not None
                                and isinstance(normal_avg_duration, (int, float))
                                and isinstance(abnormal_avg_duration, (int, float))
                            ):
                                duration_change = float(abnormal_avg_duration) - float(normal_avg_duration)
                                st.metric(
                                    "Avg Duration Change",
                                    f"{duration_change:+.2f}ms",
                                    f"{abnormal_avg_duration:.2f}ms vs {normal_avg_duration:.2f}ms",
                                )
                            else:
                                st.metric("Avg Duration Change", "N/A", "Insufficient data")

                        # Top problematic spans
                        st.subheader("Most Impacted Spans")

                        # Join the data to find spans that exist in both periods for comparison
                        normal_span_lookup = {row["span_name"]: row for row in normal_stats.iter_rows(named=True)}

                        impact_analysis = []
                        for abnormal_row in abnormal_stats.iter_rows(named=True):
                            span_name = abnormal_row["span_name"]
                            if span_name in normal_span_lookup:
                                normal_row = normal_span_lookup[span_name]

                                # Safely calculate differences with type checking
                                abnormal_error_rate = (
                                    float(abnormal_row["error_rate_pct"])
                                    if abnormal_row["error_rate_pct"] is not None
                                    else 0.0
                                )
                                normal_error_rate = (
                                    float(normal_row["error_rate_pct"])
                                    if normal_row["error_rate_pct"] is not None
                                    else 0.0
                                )
                                error_rate_increase = abnormal_error_rate - normal_error_rate

                                abnormal_duration = (
                                    float(abnormal_row["avg_duration_ms"])
                                    if abnormal_row["avg_duration_ms"] is not None
                                    else 0.0
                                )
                                normal_duration = (
                                    float(normal_row["avg_duration_ms"])
                                    if normal_row["avg_duration_ms"] is not None
                                    else 0.0
                                )
                                duration_increase = abnormal_duration - normal_duration

                                # Calculate percentile differences
                                abnormal_p99 = (
                                    float(abnormal_row["p99_duration_ms"])
                                    if abnormal_row["p99_duration_ms"] is not None
                                    else 0.0
                                )
                                normal_p99 = (
                                    float(normal_row["p99_duration_ms"])
                                    if normal_row["p99_duration_ms"] is not None
                                    else 0.0
                                )
                                p99_increase = abnormal_p99 - normal_p99

                                abnormal_p95 = (
                                    float(abnormal_row["p95_duration_ms"])
                                    if abnormal_row["p95_duration_ms"] is not None
                                    else 0.0
                                )
                                normal_p95 = (
                                    float(normal_row["p95_duration_ms"])
                                    if normal_row["p95_duration_ms"] is not None
                                    else 0.0
                                )
                                p95_increase = abnormal_p95 - normal_p95

                                abnormal_p90 = (
                                    float(abnormal_row["p90_duration_ms"])
                                    if abnormal_row["p90_duration_ms"] is not None
                                    else 0.0
                                )
                                normal_p90 = (
                                    float(normal_row["p90_duration_ms"])
                                    if normal_row["p90_duration_ms"] is not None
                                    else 0.0
                                )
                                p90_increase = abnormal_p90 - normal_p90

                                impact_analysis.append(
                                    {
                                        "Span Name": span_name,
                                        "Error Rate Increase (%)": f"{error_rate_increase:.2f}",
                                        "Duration Increase (ms)": f"{duration_increase:.2f}",
                                        "P90 Increase (ms)": f"{p90_increase:.2f}",
                                        "P95 Increase (ms)": f"{p95_increase:.2f}",
                                        "P99 Increase (ms)": f"{p99_increase:.2f}",
                                        "Normal Error Rate (%)": f"{normal_error_rate:.2f}",
                                        "Abnormal Error Rate (%)": f"{abnormal_error_rate:.2f}",
                                        "Normal Avg Duration (ms)": f"{normal_duration:.2f}",
                                        "Abnormal Avg Duration (ms)": f"{abnormal_duration:.2f}",
                                        "Normal P99 (ms)": f"{normal_p99:.2f}",
                                        "Abnormal P99 (ms)": f"{abnormal_p99:.2f}",
                                    }
                                )

                        if impact_analysis:
                            impact_df = pl.DataFrame(impact_analysis)
                            # Sort by error rate increase descending
                            impact_df = impact_df.sort("Error Rate Increase (%)", descending=True)
                            st.dataframe(impact_df.to_pandas(), width="stretch")
                        else:
                            st.info("No overlapping spans found between normal and abnormal periods for comparison")

                except Exception as e:
                    st.error(f"Error generating comparison summary: {str(e)}")

            col_normal, col_abnormal = st.columns(2)

            with col_normal:
                st.subheader("Normal Period")

                if len(normal_traces_df) > 0 and selected_service in normal_services:
                    with st.spinner("Aggregating normal spans..."):
                        normal_stats = visualizer.get_span_aggregated_stats(normal_traces_df, selected_service)

                    if len(normal_stats) > 0:
                        # Summary metrics (compact layout)
                        total_calls = normal_stats["total_calls"].sum()
                        total_errors = normal_stats["error_count"].sum()
                        avg_error_rate = (total_errors / total_calls * 100) if total_calls > 0 else 0
                        avg_duration = normal_stats["avg_duration_ms"].mean()

                        # Compact metrics in one row
                        metric_col1, metric_col2, metric_col3 = st.columns(3)
                        metric_col1.metric("Total Calls", f"{total_calls:,}")
                        metric_col2.metric("Error Rate", f"{avg_error_rate:.2f}%")
                        metric_col3.metric(
                            "Avg Duration",
                            f"{avg_duration:.2f}ms" if avg_duration else "N/A",
                        )

                        # Format for display
                        normal_display = visualizer.format_aggregated_stats_for_display(normal_stats)
                        st.dataframe(normal_display.to_pandas(), height=400, width="stretch")
                    else:
                        st.info("No span data available for this service in normal period")
                else:
                    st.info("No normal trace data available for this service")

            with col_abnormal:
                st.subheader("Abnormal Period")

                if len(abnormal_traces_df) > 0 and selected_service in abnormal_services:
                    with st.spinner("Aggregating abnormal spans..."):
                        abnormal_stats = visualizer.get_span_aggregated_stats(abnormal_traces_df, selected_service)

                    if len(abnormal_stats) > 0:
                        # Summary metrics (compact layout)
                        total_calls = abnormal_stats["total_calls"].sum()
                        total_errors = abnormal_stats["error_count"].sum()
                        avg_error_rate = (total_errors / total_calls * 100) if total_calls > 0 else 0
                        avg_duration = abnormal_stats["avg_duration_ms"].mean()

                        # Compact metrics in one row
                        metric_col1, metric_col2, metric_col3 = st.columns(3)
                        metric_col1.metric("Total Calls", f"{total_calls:,}")
                        metric_col2.metric("Error Rate", f"{avg_error_rate:.2f}%")
                        metric_col3.metric(
                            "Avg Duration",
                            f"{avg_duration:.2f}ms" if avg_duration else "N/A",
                        )

                        # Format for display
                        abnormal_display = visualizer.format_aggregated_stats_for_display(abnormal_stats)
                        st.dataframe(abnormal_display.to_pandas(), height=400, width="stretch")
                    else:
                        st.info("No span data available for this service in abnormal period")
                else:
                    st.info("No abnormal trace data available for this service")

    except Exception as e:
        st.error(f"Error occurred while analyzing trace data: {str(e)}")
        st.info("Tip: Please ensure the dataset contains valid trace data")
        with st.expander("Debug Information"):
            st.exception(e)


def render_annotations_tab():
    if not st.session_state.dataset_loaded:
        st.warning("Please load a dataset in the sidebar first")
        st.info("Enter the dataset path in the sidebar and click 'Load Dataset' to begin.")
        return

    st.header("Annotations Management")

    data_loader = get_data_loader()
    dataset_path = get_current_dataset_path()
    data_loader.set_dataset_path(dataset_path)
    label_manager = get_label_manager()

    if not dataset_path:
        return

    st.subheader("Existing Annotations")

    if "annotations_data" not in st.session_state or st.session_state.get("annotations_refresh", False):
        annotations = label_manager.get_annotations(str(dataset_path))
        st.session_state.annotations_data = annotations
        st.session_state.annotations_refresh = False
    else:
        annotations = st.session_state.annotations_data

    if len(annotations) > 0:
        for annotation in annotations.iter_rows(named=True):
            with st.expander(f"{annotation['annotation_type']}: {annotation['annotation_value']}"):
                col1, col2 = st.columns([3, 1])

                with col1:
                    st.write(f"**Confidence:** {annotation['confidence']}")
                    st.write(f"**Notes:** {annotation['notes']}")
                    st.write(f"**Created At:** {annotation['created_at']}")

                with col2:
                    # Use a form for delete to avoid accidental clicks
                    with st.form(f"delete_annotation_{annotation['id']}"):
                        delete_submitted = st.form_submit_button("Delete", type="secondary", width="stretch")
                        if delete_submitted:
                            if label_manager.delete_annotation(annotation["id"]):
                                st.success("Annotation deleted successfully!")
                                st.session_state.annotations_refresh = True
                                st.rerun()
                            else:
                                st.error("Failed to delete annotation")
    else:
        st.info("No annotations available")

    st.subheader("Add New Annotation")

    # Use form for adding annotations
    with st.form("add_annotation_form"):
        col1, col2 = st.columns(2)

        with col1:
            annotation_type = st.selectbox(
                "Annotation Type",
                ["root_cause", "anomaly_type", "fault_category", "severity", "custom"],
                format_func=lambda x: {
                    "root_cause": "Root Cause",
                    "anomaly_type": "Anomaly Type",
                    "fault_category": "Fault Category",
                    "severity": "Severity",
                    "custom": "Custom",
                }[x],
            )

            custom_type = None
            if annotation_type == "custom":
                custom_type = st.text_input("Custom Type")

        with col2:
            confidence = st.slider("Confidence", 0.0, 1.0, 1.0, 0.1)

        annotation_value = st.text_input("Annotation Value")
        notes = st.text_area("Notes", height=100)

        add_submitted = st.form_submit_button("Add Annotation", type="primary")

        if add_submitted:
            final_annotation_type = custom_type if annotation_type == "custom" else annotation_type
            if final_annotation_type and annotation_value:
                if label_manager.add_annotation(
                    str(dataset_path),
                    final_annotation_type,
                    annotation_value,
                    confidence,
                    notes,
                ):
                    st.success("Annotation added successfully!")
                    st.session_state.annotations_refresh = True
                    st.rerun()
            else:
                st.error("Please fill in the required fields")


def main() -> None:
    try:
        configure_page()
        render_sidebar()
        tab1, tab2, tab3, tab4, tab5 = st.tabs(["Metadata", "Metrics", "Logs", "Traces", "Annotations"])
        current_tab = None

        with tab1:
            current_tab = "metadata"
            render_metadata_tab()

        with tab2:
            current_tab = "metrics"
            if st.session_state.dataset_loaded:
                render_metrics_tab()
            else:
                st.warning("Please load a dataset in the sidebar first")

        with tab3:
            current_tab = "logs"
            if st.session_state.dataset_loaded:
                render_logs_tab()
            else:
                st.warning("Please load a dataset in the sidebar first")

        with tab4:
            current_tab = "traces"
            if st.session_state.dataset_loaded:
                render_traces_tab()
            else:
                st.warning("Please load a dataset in the sidebar first")

        with tab5:
            current_tab = "annotations"
            if st.session_state.dataset_loaded:
                render_annotations_tab()
            else:
                st.warning("Please load a dataset in the sidebar first")

        if current_tab:
            st.session_state.last_tab_active = current_tab

    except Exception as e:
        st.error(f"Application error: {str(e)}")
        with st.expander("Debug Information"):
            st.exception(e)


def run_streamlit():
    app_path = Path(__file__)
    subprocess.run([sys.executable, "-m", "streamlit", "run", str(app_path)])


if __name__ == "__main__":
    main()
